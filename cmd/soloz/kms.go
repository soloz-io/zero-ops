package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/platform/kms"
	"github.com/spf13/cobra"
)

var (
	kmsProjectID     string
	kmsProjectNumber string
	kmsLocation      string
	kmsKeyRing       string
	kmsKey           string
	kmsCluster       string
	kmsServiceAcct   string
	kmsRotation      time.Duration
	kmsDestroyWindow time.Duration
	kmsOut           string
	kmsImpersonate   string
	kmsGrantImpTo    string
	kmsDenyProbe     bool
	kmsSkipAPIEnable bool
	kmsRevokeMember  string
	kmsCanary        bool
	kmsDesiredFrom   string
	kmsCAFile        string
	kmsPool          string
	kmsProvider      string
	kmsJWKSFile      string
	kmsFormat        string
)

// newKMSCmd manages the Google Cloud KMS resources ADR-100 depends on.
//
// WHY THIS IS A COMMAND. A key created by hand is created differently each time: the
// rotation period gets forgotten, the IAM binding lands at the PROJECT instead of the
// key, and a service-account JSON key appears "just for testing". ADR-100's
// least-privilege claim is only as good as the policy somebody actually applied, and
// the only way a claim like that holds is to stop a human being the thing that applies
// it.
//
// The escrow is the precedent (`soloz escrow init`): the account is irreducibly manual,
// everything after it is not. Here even the account is not -- what stays manual is the
// GCP project itself, and nothing else.
func newKMSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kms",
		Short: "Provision and inspect the Cloud KMS keys that encrypt etcd (ADR-100)",
		Long: `Manage the external key authority for encryption at rest.

One key per cluster, non-exportable, never copied. The plugin's identity holds exactly
` + kms.RoleEncrypterDecrypter + ` on ONE key and no key file exists for
it — ADR-100 prohibits one, so this command cannot create one.

Every subcommand is idempotent. Re-running confirms state rather than creating a second
anything.`,
	}
	cmd.AddCommand(newKMSInitCmd())
	cmd.AddCommand(newKMSKeyRingCmd())
	cmd.AddCommand(newKMSKeyCmd())
	cmd.AddCommand(newKMSVerifyCmd())
	cmd.AddCommand(newKMSProveCmd())
	cmd.AddCommand(newKMSRevokeCmd())
	cmd.AddCommand(newKMSDriftCmd())
	cmd.AddCommand(newKMSTrustAnchorCmd())
	cmd.AddCommand(newKMSJWKSCmd())
	cmd.AddCommand(newKMSCheckCmd())

	// Shared across every subcommand: which project, and where to write what was done.
	pf := cmd.PersistentFlags()
	pf.StringVar(&kmsProjectID, "project", "", "GCP project id (required; or k8-secrets/gcp/project-id)")
	pf.StringVar(&kmsProjectNumber, "project-number", "",
		"GCP project number, needed only to enable the KMS API (or k8-secrets/gcp/project-number)")
	pf.StringVar(&kmsLocation, "location", "europe-west3",
		"key location. ADR-100 proposes europe-west3 (Frankfurt), regional: closest to the "+
			"Hetzner sites and keeps residency in the EU with the clusters it protects")
	pf.StringVar(&kmsKeyRing, "keyring", "soloz-etcd", "key ring id")
	pf.StringVar(&kmsOut, "out", "k8-secrets/gcp", "directory the resource names are written to")
	pf.StringVar(&kmsImpersonate, "impersonate", "",
		"act as this service account instead of your own credential. This is how the "+
			"least-privilege claim is TESTED: an owner is denied nothing, so the negative "+
			"checks must run as the plugin's own identity")
	return cmd
}

// resolveProject fills --project / --project-number from the files on disk.
//
// The same directory the escrow writes to, for the same reason: a value the tooling
// already has should not be a value the operator retypes. Retyping a project id is how
// you get a 403 that reads as an IAM problem.
func resolveProject() error {
	if kmsProjectID == "" {
		kmsProjectID = readTrimmed(filepath.Join(kmsOut, "project-id"))
	}
	if kmsProjectNumber == "" {
		kmsProjectNumber = readTrimmed(filepath.Join(kmsOut, "project-number"))
	}
	if kmsProjectID == "" {
		return fmt.Errorf("no project id: pass --project, or put it in %s",
			filepath.Join(kmsOut, "project-id"))
	}
	return nil
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func newKMSInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Establish one cluster's key, its identity, and the binding between them",
		Long: `Create everything one cluster needs, idempotently and in dependency order.

  1. enable cloudkms.googleapis.com
  2. the key ring                       (cannot be deleted, ever — that is a feature)
  3. the CryptoKey                      rotation period and destroy window set here,
                                        so the key cannot exist in a state where
                                        nobody chose them
  4. the plugin's service account       NO key file. ADR-100 prohibits one and this
                                        command offers no flag to make one
  5. the IAM binding AT THE KEY         not at the project. The project is what the
                                        console offers first and it grants encrypt and
                                        decrypt on every key in it

With --with-deny-probe it also creates a second key in the same ring that the identity
is deliberately NOT bound to, so cross-key denial can be asserted instead of assumed.

With --grant-impersonation-to it lets you assume the identity, which is what makes the
negative evidence collectable before Workload Identity Federation exists. That is an
escalation path: revoke it when the evidence is in, and ` + "`soloz kms verify`" + ` prints
who holds it so it is a visible decision rather than a forgotten one.

Re-running changes nothing and confirms everything.`,
		RunE: runKMSInit,
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster this key belongs to (required)")
	f.StringVar(&kmsKey, "key", "", "CryptoKey id (default: etcd-kek-<cluster>)")
	f.StringVar(&kmsServiceAcct, "service-account", "",
		"service account id (default: kms-<cluster>)")
	f.DurationVar(&kmsRotation, "rotation-period", kms.DefaultRotationPeriod,
		"how often the key rotates. ADR-100 caps this at 90 days")
	f.DurationVar(&kmsDestroyWindow, "destroy-window", kms.DefaultDestroyScheduledDuration,
		"how long a destroy stays pending and recoverable. Destroying a version makes every "+
			"backup taken under it unreadable, so longer is safer than shorter")
	f.StringVar(&kmsGrantImpTo, "grant-impersonation-to", "",
		"principal that may assume the identity, e.g. user:you@example.com")
	f.BoolVar(&kmsDenyProbe, "with-deny-probe", false,
		"also create an UNBOUND second key, so cross-key denial can be tested. Without it "+
			"there is nothing for the identity to be denied on")
	f.BoolVar(&kmsCanary, "with-canary", false,
		"also create a disposable third key carrying the IDENTICAL binding — same service "+
			"account, same single role, at the key. Administrative denials (rotate, disable, "+
			"destroy, administer, alter IAM) are proven against it with real calls, because "+
			"attempting them on the live key performs them when the policy is wrong, and "+
			"Google documents testIamPermissions as unsuitable for authorization checking")
	f.BoolVar(&kmsSkipAPIEnable, "skip-api-enable", false,
		"skip enabling cloudkms.googleapis.com (for a project where you lack serviceusage)")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func runKMSInit(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if err := resolveProject(); err != nil {
		return err
	}
	if kmsRotation > kms.DefaultRotationPeriod {
		return fmt.Errorf("--rotation-period %s exceeds ADR-100's ceiling of %s. The API "+
			"server retains data keys in memory, so a key's exposure window is otherwise "+
			"unbounded", kmsRotation, kms.DefaultRotationPeriod)
	}
	if kmsKey == "" {
		kmsKey = kms.KeyIDFor(kmsCluster)
	}
	if kmsServiceAcct == "" {
		kmsServiceAcct = kms.ServiceAccountIDFor(kmsCluster)
	}

	c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
	if err != nil {
		return err
	}

	out, err := c.Provision(ctx, kms.ProvisionRequest{
		Spec: kms.KeySpec{
			Location:                 kmsLocation,
			KeyRing:                  kmsKeyRing,
			Key:                      kmsKey,
			RotationPeriod:           kmsRotation,
			DestroyScheduledDuration: kmsDestroyWindow,
		},
		ServiceAccountID:    kmsServiceAcct,
		ImpersonationMember: kmsGrantImpTo,
		WithDenyProbe:       kmsDenyProbe,
		WithCanary:          kmsCanary,
		SkipAPIEnable:       kmsSkipAPIEnable,
	})
	if err != nil {
		return err
	}

	if err := writeKMSOutputs(kmsCluster, out); err != nil {
		return err
	}

	fmt.Printf("✓ %s\n", out.CryptoKey)
	fmt.Printf("  identity   %s (%s, at the key)\n", out.ServiceAccount, kms.RoleEncrypterDecrypter)
	fmt.Printf("  rotation   every %s, destroy pending for %s\n", kmsRotation, kmsDestroyWindow)
	if out.DenyProbeKey != "" {
		fmt.Printf("  deny probe %s (deliberately UNBOUND — proves cross-key denial)\n",
			out.DenyProbeKey)
	}
	if out.CanaryKey != "" {
		fmt.Printf("  canary     %s (bound IDENTICALLY, holds no data — proves administrative denial)\n",
			out.CanaryKey)
	}
	if kmsGrantImpTo != "" {
		fmt.Printf("  %s may assume that identity — revoke it when the evidence is in\n", kmsGrantImpTo)
	}
	fmt.Printf("  written to %s/\n", kmsOut)
	fmt.Println()
	fmt.Println("  KMS_CRYPTO_KEY for this cluster's control-plane template:")
	fmt.Printf("    %s\n", out.CryptoKey)
	fmt.Println()
	fmt.Println("  No key file was created and none can be (ADR-100). The identity is assumed:")
	fmt.Println("  by impersonation today, by Workload Identity Federation once gate 3 decides")
	fmt.Println("  how a static pod presents an external identity.")
	return nil
}

// writeKMSOutputs records what exists, per cluster.
//
// Per cluster and not one shared file: two clusters have two keys and two identities,
// and a single `crypto-key` file is how they end up sharing one.
func writeKMSOutputs(cluster string, out *kms.Provisioned) error {
	dir := filepath.Join(kmsOut, cluster)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	files := map[string]string{
		"KMS_CRYPTO_KEY":      out.CryptoKey,
		"KMS_KEY_RING":        out.KeyRing,
		"KMS_SERVICE_ACCOUNT": out.ServiceAccount,
		"GCP_PROJECT_ID":      out.ProjectID,
	}
	if out.DenyProbeKey != "" {
		files["KMS_DENY_PROBE_KEY"] = out.DenyProbeKey
	}
	if out.CanaryKey != "" {
		files["KMS_CANARY_KEY"] = out.CanaryKey
	}
	for name, value := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}
	return nil
}

// newKMSKeyRingCmd and newKMSKeyCmd are the granular halves of init.
//
// init is what gets used; these exist because a key ring and a key have different
// lifetimes -- one ring outlives every cluster in it -- and because adding a key to an
// existing ring should not require re-running everything else.
func newKMSKeyRingCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "keyrings", Short: "Manage key rings"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create the key ring (idempotent; a key ring can never be deleted)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := resolveProject(); err != nil {
				return err
			}
			c, err := kms.New(cmd.Context(), kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			name, err := c.EnsureKeyRing(cmd.Context(), kmsLocation, kmsKeyRing)
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s\n", name)
			return nil
		},
	}
	cmd.AddCommand(create)
	return cmd
}

func newKMSKeyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "keys", Short: "Manage keys"}
	create := &cobra.Command{
		Use:   "create <key-id>",
		Short: "Create one key in the ring (idempotent; verifies rather than rewrites)",
		Long: `Create a CryptoKey with its rotation period and destroy window set.

If the key already exists this VERIFIES it and reports any difference without changing
anything. ADR-100 puts ongoing reconciliation in Crossplane; a bootstrap command that
silently rewrote the properties of a key already holding data would be a second,
competing reconciler.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := resolveProject(); err != nil {
				return err
			}
			c, err := kms.New(cmd.Context(), kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			name, err := c.EnsureCryptoKey(cmd.Context(), kms.KeySpec{
				Location:                 kmsLocation,
				KeyRing:                  kmsKeyRing,
				Key:                      args[0],
				RotationPeriod:           kmsRotation,
				DestroyScheduledDuration: kmsDestroyWindow,
			})
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s\n", name)
			return nil
		},
	}
	f := create.Flags()
	f.DurationVar(&kmsRotation, "rotation-period", kms.DefaultRotationPeriod, "rotation period")
	f.DurationVar(&kmsDestroyWindow, "destroy-window", kms.DefaultDestroyScheduledDuration,
		"how long a destroy stays pending")
	cmd.AddCommand(create)
	return cmd
}

// newKMSVerifyCmd prints the policy ADR-100's least-privilege claim is about.
//
// A claim nobody can read is a claim nobody checks. This prints the key's actual IAM
// bindings and who can assume the plugin's identity, so "scoped to one key" is an
// output rather than a sentence in a document.
func newKMSVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Print what actually exists: the key, its rotation, and its exact IAM bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsKey == "" {
				if kmsCluster == "" {
					return fmt.Errorf("pass --cluster or --key")
				}
				kmsKey = kms.KeyIDFor(kmsCluster)
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			name := c.CryptoKeyName(kmsLocation, kmsKeyRing, kmsKey)

			key, err := c.GetCryptoKey(ctx, name)
			if err != nil {
				return fmt.Errorf("reading %s: %w", name, err)
			}
			fmt.Printf("%s\n", key.Name)
			fmt.Printf("  purpose                  %s\n", key.Purpose)
			fmt.Printf("  rotationPeriod           %s\n", key.RotationPeriod)
			fmt.Printf("  destroyScheduledDuration %s\n", key.DestroyScheduledDuration)
			fmt.Printf("  primary                  %s (%s)\n", key.Primary.Name, key.Primary.State)

			bindings, err := c.KeyBindings(ctx, name)
			if err != nil {
				return err
			}
			fmt.Println("\n  IAM, at the key:")
			printBindings(bindings)
			fmt.Println("\n  Anything other than " + kms.RoleEncrypterDecrypter +
				" on the plugin's identity is wider than ADR-100 claims.")

			if kmsCluster != "" {
				sa := c.ServiceAccountEmail(kms.ServiceAccountIDFor(kmsCluster))
				saB, err := c.ServiceAccountBindings(ctx, sa)
				if err == nil {
					fmt.Printf("\n  Who can act as %s:\n", sa)
					if len(saB) == 0 {
						fmt.Println("    (nobody — as it should be once the evidence is collected)")
					}
					printBindings(saB)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose key to verify")
	f.StringVar(&kmsKey, "key", "", "CryptoKey id (default: etcd-kek-<cluster>)")
	return cmd
}

func printBindings(b map[string][]string) {
	roles := make([]string, 0, len(b))
	for r := range b {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		members := b[r]
		sort.Strings(members)
		fmt.Printf("    %-52s %s\n", r, strings.Join(members, ", "))
	}
}

// newKMSProveCmd collects the evidence ADR-100's identity claim rests on.
//
// WHY A COMMAND AND NOT A RUNBOOK OF gcloud CALLS. The claim is "this identity holds
// encrypt and decrypt on ONE key and nothing else". Every part of that is a negative,
// and negatives are what nobody re-checks after the first time. A command re-checks
// them on every key, in the same order, and prints rows somebody can paste into a
// review.
//
// IT MUST RUN AS THE PLUGIN'S IDENTITY. Run as a project owner, every negative passes
// trivially because nothing is denied, and the output is worthless while looking
// perfect. So --impersonate is REQUIRED here, unlike every other subcommand, and the
// output says what it proves and what it does not.
func newKMSProveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prove",
		Short: "Prove the key-scoped IAM policy: what the plugin's identity can and cannot do",
		Long: `Run the positive and negative checks ADR-100's least-privilege claim depends on.

POSITIVE — exactly what the plugin does, in the order it does it:
  discover the active version by encrypting against the parent key
  wrap a data encryption key under that exact version
  unwrap it, and confirm it survives byte for byte

NEGATIVE — the denials the claim is made of:
  GetCryptoKey                 needs cryptoKeys.get, which the role does not carry
  a different key in the ring  cross-key denial
  updatePrimaryVersion         the call that could reactivate retired key material

This must run AS the plugin's identity, so --impersonate is required. Grant yourself
that with ` + "`soloz kms init --grant-impersonation-to`" + `, and revoke it once the
evidence is collected — ` + "`soloz kms verify`" + ` prints who holds it.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" {
				return fmt.Errorf("--cluster is required")
			}
			if kmsImpersonate == "" {
				// REFUSED RATHER THAN WARNED. A run without impersonation produces a
				// full set of passing rows that prove nothing, and that output is worse
				// than no output: somebody will paste it into a review.
				return fmt.Errorf("--impersonate is required for `prove`.\n\n"+
					"Run as yourself, every negative check passes because you are denied "+
					"nothing, and the result is worthless while looking perfect. The claim "+
					"is about the PLUGIN's identity, so the checks have to be it:\n\n"+
					"  --impersonate %s\n",
					"kms-"+kmsCluster+"@"+kmsProjectID+".iam.gserviceaccount.com")
			}

			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			if kmsKey == "" {
				kmsKey = kms.KeyIDFor(kmsCluster)
			}
			keyName := c.CryptoKeyName(kmsLocation, kmsKeyRing, kmsKey)

			// The deny-probe key, read from what `init` wrote rather than guessed, so a
			// run against a key provisioned without one says so instead of inventing a
			// name and reporting NOT_FOUND as if it were a denial.
			probe := readTrimmed(filepath.Join(kmsOut, kmsCluster, "KMS_DENY_PROBE_KEY"))
			// Read from what `init` wrote rather than derived, so a run against a key
			// provisioned without a canary says "not-tested" instead of inventing a name
			// and reporting NOT_FOUND as though it were a denial.
			canary := readTrimmed(filepath.Join(kmsOut, kmsCluster, "KMS_CANARY_KEY"))

			fmt.Printf("as  %s\n", kmsImpersonate)
			fmt.Printf("key %s\n\n", keyName)

			checks := c.ProveLeastPrivilege(ctx, keyName, canary, probe)
			failed := 0
			// COLOUR ONLY WHEN A HUMAN IS LOOKING. This output is evidence: ADR-100's
			// review asked for a key-scoped IAM policy demonstrated against a real
			// project, and the rows below are what satisfies that. Piped to a file or a
			// clipboard they get pasted into the ADR, and escape codes in a document
			// are worse than no colour -- they obscure the one thing somebody is
			// reading it for.
			pass, fail := "PASS", "FAIL"
			if isTerminal(os.Stdout) {
				pass, fail = "\033[32mPASS\033[0m", "\033[31mFAIL\033[0m"
			}
			for _, k := range checks {
				mark := pass
				if !k.OK {
					mark, failed = fail, failed+1
				}
				fmt.Printf("  %s  %-62s want %-6s got %s\n", mark, k.What, k.Want, k.Got)
				if k.Detail != "" {
					fmt.Printf("        %s\n", k.Detail)
				}
			}

			fmt.Printf("\n%d of %d\n", len(checks)-failed, len(checks))
			fmt.Println()
			fmt.Println("Proves, by REAL calls on the enforcement path: this identity can encrypt")
			fmt.Println("and decrypt under its own key, and is denied metadata reads, another key,")
			fmt.Println("and every administrative operation. The administrative rows run against a")
			fmt.Println("canary carrying the IDENTICAL binding, so they are denied for the")
			fmt.Println("production reason — and because they exercise enforcement rather than")
			fmt.Println("policy, an inherited project-level grant would make them SUCCEED.")
			fmt.Println()
			fmt.Println("Does NOT prove: Workload Identity Federation (this used impersonation),")
			fmt.Println("credential expiry or revocation behaviour, quotas, or latency.")
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed: the policy is not what ADR-100 claims", failed)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose key to prove (required)")
	f.StringVar(&kmsKey, "key", "", "CryptoKey id (default: etcd-kek-<cluster>)")
	return cmd
}

// isTerminal reports whether f is attached to a terminal.
//
// os.Stat rather than golang.org/x/term: this needs one bit of information and does not
// need a dependency to get it.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// newKMSRevokeCmd removes the temporary impersonation grant.
//
// A SEPARATE COMMAND BECAUSE IT IS A SEPARATE DECISION. `kms init
// --grant-impersonation-to` opens an escalation path so the least-privilege claim can be
// tested at all; it should not outlive the evidence. Leaving the revocation to whoever
// remembers means a standing ability to assume the key's identity, which is most of what
// ADR-100 is trying to avoid.
func newKMSRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke-impersonation",
		Short: "Remove a principal's ability to assume the plugin's identity",
		Long: `Remove one member from roles/iam.serviceAccountTokenCreator on the plugin's
service account, leaving every other binding intact.

Read-modify-write, deliberately. The first time this was revoked it was done by hand with
a setIamPolicy carrying an empty binding list — correct for a policy with exactly one
binding, and silently destructive for any other. Removing a member that is not there is a
no-op, so this is safe to run twice.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" || kmsRevokeMember == "" {
				return fmt.Errorf("--cluster and --member are both required")
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, "")
			if err != nil {
				return err
			}
			sa := c.ServiceAccountEmail(kms.ServiceAccountIDFor(kmsCluster))

			changed, err := c.RevokeImpersonation(ctx, sa, kmsRevokeMember)
			if err != nil {
				return err
			}
			if changed {
				fmt.Printf("✓ %s can no longer assume %s\n", kmsRevokeMember, sa)
			} else {
				fmt.Printf("· %s already could not assume %s; nothing changed\n", kmsRevokeMember, sa)
			}

			// Read it back. The point of this command is that an escalation path is
			// closed, and that claim is worth one extra call rather than an assumption.
			bindings, err := c.ServiceAccountBindings(ctx, sa)
			if err != nil {
				return err
			}
			fmt.Printf("\n  Who can act as %s:\n", sa)
			if len(bindings) == 0 {
				fmt.Println("    (nobody — as it should be once the evidence is collected)")
			}
			printBindings(bindings)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose identity to revoke on (required)")
	f.StringVar(&kmsRevokeMember, "member", "",
		"principal to remove, e.g. user:you@example.com (required)")
	return cmd
}

// newKMSDriftCmd compares the live key against the reviewed desired state in Git.
//
// IT DETECTS AND DOES NOT REPAIR, and that is the decision rather than an unfinished
// feature. ADR-100 makes the in-cluster reconciler observe-only: to update the key it
// would need cloudkms.cryptoKeys.update on the key encrypting its own cluster's etcd. A
// command that quietly fixed drift would hand back exactly the mutation authority that
// was withheld, and would do it where nobody is looking.
//
// It runs from OUTSIDE the cluster with an operator credential, which is also where the
// remediation it asks for happens.
func newKMSDriftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Compare the live key against the desired state in Git, and alarm on a difference",
		Long: `Report differences between what Git declares and what Cloud KMS actually has.

Two kinds of finding:

  ALARM   desired and authoritative state differ, or the primary version has moved
          BACKWARDS onto retired material. Exits non-zero.
  NOTICE  a rotation landed and the version floor has not been recorded yet. Rotation is
          supposed to happen, so this does not fail — it prints the line to commit.

Nothing is repaired. The version floor is an annotation on the Crossplane manifest, so
advancing it is a commit somebody approves; a floor that this command moved on its own
would be a record that agrees with whatever it finds.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" {
				return fmt.Errorf("--cluster is required")
			}
			expected, err := kms.ParseExpected(kmsDesiredFrom, kmsCluster)
			if err != nil {
				return err
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			if kmsKey == "" {
				kmsKey = kms.KeyIDFor(kmsCluster)
			}
			keyName := c.CryptoKeyName(kmsLocation, kmsKeyRing, kmsKey)

			findings, err := c.Drift(ctx, keyName, expected)
			if err != nil {
				return err
			}

			fmt.Printf("desired  %s\n", expected.Source)
			fmt.Printf("actual   %s\n\n", keyName)
			if len(findings) == 0 {
				fmt.Println("  no drift: the live key matches the reviewed desired state, and the")
				fmt.Printf("  primary version is at the recorded floor (%d).\n", expected.VersionFloor)
				return nil
			}
			for _, f := range findings {
				fmt.Printf("  %-6s %s\n", f.Severity, f.What)
				fmt.Printf("         desired %s\n", f.Desired)
				fmt.Printf("         actual  %s\n", f.Actual)
				fmt.Printf("         %s\n\n", f.Action)
			}

			if kms.Alarming(findings) {
				// THE ALARM TEXT IS DELIBERATE. An operator reading this at 3am needs to
				// know in one line that the cluster is still serving -- encryption keeps
				// working off cached data keys and the key itself is intact -- so this is
				// not an outage to chase, it is a state to correct.
				return fmt.Errorf("KMS configuration drift detected; cluster encryption remains " +
					"operational, but desired and authoritative KMS state differ. Manual " +
					"platform-security remediation required")
			}
			fmt.Println("  No alarm: the notices above are changes to record, not to repair.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose key to check (required)")
	f.StringVar(&kmsKey, "key", "", "CryptoKey id (default: etcd-kek-<cluster>)")
	f.StringVar(&kmsDesiredFrom, "desired-from",
		"manifests/hub-core-services/crossplane/kms/hub-key.yaml",
		"the reviewed manifest holding desired state and the version floor. Git, not a flag "+
			"default: if this compared against constants in the binary, the thing Git says "+
			"would never be checked")
	return cmd
}

// newKMSTrustAnchorCmd refuses unless this cluster's CA is the federation provider's
// trust anchor.
//
// IT EXISTS BECAUSE A REBUILD BREAKS THIS SILENTLY. A rebuilt cluster has a NEW
// certificate authority; the provider still holds the old one. Every certificate the new
// control plane presents then fails validation, the plugin cannot authenticate, and the
// API server cannot decrypt a single Secret — and the first symptom is a KMS provider
// reporting unhealthy, which reads as a plugin bug rather than as a step somebody
// skipped.
//
// ADR-100 chose re-uploading the trust anchor over escrowing the cluster CA, because it
// adds a procedure rather than a standing exposure. A procedure that fails silently when
// skipped is not good enough for a platform that claims operations are not manual, so
// this is how it fails LOUDLY and EARLY: run before configuring KMS encryption, and the
// refusal lands where somebody is typing.
func newKMSTrustAnchorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust-anchor",
		Short: "Verify the federation provider trusts this cluster's certificate authority",
		Long: `Refuse unless the workload identity provider's X.509 trust store contains this
cluster's CA.

Run it BEFORE configuring KMS encryption on a cluster, and as the mandatory step of a
rebuild. Nothing is changed: this reads the provider and compares certificate
fingerprints, so a CA reformatted by a different tool still matches while a genuinely
different CA does not.

It also refuses a provider that is missing, disabled, or configured for OIDC rather than
X.509 — each of which validates nothing, and each of which would otherwise look like a
plugin fault hours later.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" || kmsCAFile == "" {
				return fmt.Errorf("--cluster and --ca-file are both required")
			}
			ca, err := os.ReadFile(kmsCAFile)
			if err != nil {
				return fmt.Errorf("reading the cluster CA from %s: %w", kmsCAFile, err)
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			pool, provider := kmsPool, kmsProvider
			if pool == "" {
				pool = "soloz-" + kmsCluster
			}
			if provider == "" {
				provider = "control-plane"
			}

			if err := c.VerifyTrustAnchor(ctx, pool, provider, ca); err != nil {
				return err
			}
			fmt.Printf("✓ %s trusts the certificate authority in %s\n",
				c.ProviderName(pool, provider), kmsCAFile)
			fmt.Println("  A control-plane certificate signed by it will validate, so the plugin")
			fmt.Println("  can authenticate and the API server can decrypt.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose CA to check (required)")
	f.StringVar(&kmsCAFile, "ca-file", "",
		"PEM file holding this cluster's certificate authority (required). On a CAPI-managed "+
			"cluster this is the tls.crt of the <cluster>-ca Secret")
	f.StringVar(&kmsPool, "pool", "", "workload identity pool (default: soloz-<cluster>)")
	f.StringVar(&kmsProvider, "provider", "", "pool provider (default: control-plane)")
	return cmd
}

// newKMSJWKSCmd uploads a cluster's service-account signing keys to its federation
// provider, idempotently.
//
// THIS IS WHAT LETS AN UNREACHABLE CLUSTER FEDERATE. An earlier version of ADR-100 said
// OIDC would require Google to fetch the API server's discovery document, and therefore a
// publicly reachable issuer. That was wrong: Oidc.JwksJson is documented in the pinned
// client as being used INSTEAD of the discovery fetch when it is set. Uploading the key
// set removes the requirement entirely.
//
// AND IT IS A LIFECYCLE, NOT A SETUP STEP. The upload is a COPY. Cluster signing keys
// rotate, and when they do the copy is stale and every token fails validation -- which
// surfaces as a workload that cannot authenticate with nothing in the cluster having
// changed. So this is built to be re-run on a schedule: it reads first, compares key
// identifiers, and writes only on a real difference.
func newKMSJWKSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jwks",
		Short: "Upload this cluster's service-account signing keys to its federation provider",
		Long: `Keep the federation provider's copy of the cluster's JWKS current.

Google validates a projected ServiceAccount token against keys it holds, so the cluster
never has to be reachable from the internet. The cost is that the copy goes stale when the
cluster rotates its signing keys, and a stale copy fails every token.

Idempotent by content: it compares key identifiers, not serialised JSON, so a re-run
against an unchanged cluster is a single read. The write is a PATCH scoped to the JWKS
alone, so an attributeMapping or audience somebody changed deliberately is not reverted.

Obtain the key set with:

    kubectl get --raw /openid/v1/jwks > jwks.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" || kmsJWKSFile == "" {
				return fmt.Errorf("--cluster and --jwks-file are both required")
			}
			raw, err := os.ReadFile(kmsJWKSFile)
			if err != nil {
				return fmt.Errorf("reading the JWKS from %s: %w", kmsJWKSFile, err)
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			pool, provider := kmsPool, kmsProvider
			if pool == "" {
				pool = "soloz-" + kmsCluster
			}
			if provider == "" {
				// The WORKLOAD provider, not the control-plane one. The KMS plugin's
				// provider is X.509 and holds a trust anchor, not a key set; they are
				// different objects for different identities and must not be conflated.
				provider = "workloads"
			}

			out, err := c.SyncJWKS(ctx, pool, provider, raw)
			if err != nil {
				return err
			}
			if !out.Changed {
				fmt.Printf("· %s already holds these signing keys (%s); nothing written\n",
					out.Provider, strings.Join(out.After, ", "))
				return nil
			}
			fmt.Printf("✓ %s\n", out.Provider)
			fmt.Printf("  before %s\n", describeKids(out.Before))
			fmt.Printf("  after  %s\n", strings.Join(out.After, ", "))
			fmt.Println("  Re-run this after every signing-key rotation: the uploaded set is a")
			fmt.Println("  copy, and a stale copy fails every token while the cluster looks fine.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster whose signing keys to upload (required)")
	f.StringVar(&kmsJWKSFile, "jwks-file", "",
		"the cluster's JWKS (required). `kubectl get --raw /openid/v1/jwks > jwks.json`")
	f.StringVar(&kmsPool, "pool", "", "workload identity pool (default: soloz-<cluster>)")
	f.StringVar(&kmsProvider, "provider", "",
		"pool provider (default: workloads — NOT the X.509 control-plane provider)")
	return cmd
}

func describeKids(kids []string) string {
	if len(kids) == 0 {
		return "(none)"
	}
	return strings.Join(kids, ", ")
}

// newKMSCheckCmd runs every observation for one cluster and emits a routable result.
//
// THE CARRIER'S ENTRY POINT. `drift`, `jwks` and `trust-anchor` are for a person; this is
// for a scheduler. A carrier that scraped the human output would break the first time a
// message was reworded, and those messages are deliberately written for somebody reading
// them at 03:00.
//
// THREE EXIT CODES, AND THE THIRD IS THE ONE THAT MATTERS:
//
//	0  healthy, or notices only. Forward rotation is expected and must not page.
//	1  ALARM. Desired and authoritative state differ, or an identity will stop working.
//	2  UNKNOWN. A check could NOT RUN. Not success, and not the same alert as a drift:
//	   the thing being checked may be perfectly fine and the CHECK may be broken.
//
// Collapsing 2 into 0 would report a healthy security control whenever the control itself
// was broken, which is the failure this ADR has now found in four different places.
//
// IT OBSERVES AND NEVER MUTATES. A test asserts every request it makes is a GET.
func newKMSCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run every KMS and federation observation for a cluster, for a scheduler to route",
		Long: `Produce one machine-readable result covering the key, the uploaded signing keys and
the plugin's trust anchor.

Exit codes: 0 healthy or notice, 1 alarm, 2 a check could not run.

Inputs that are absent downgrade a check to UNKNOWN rather than skipping it. A carrier
that cannot reach a cluster still learns that it could not, which is the difference
between "the signing keys are current" and "nobody looked".`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := resolveProject(); err != nil {
				return err
			}
			if kmsCluster == "" {
				return fmt.Errorf("--cluster is required")
			}
			c, err := kms.New(ctx, kmsProjectID, kmsProjectNumber, kmsImpersonate)
			if err != nil {
				return err
			}
			if kmsKey == "" {
				kmsKey = kms.KeyIDFor(kmsCluster)
			}
			o := kms.CheckOptions{
				Cluster:      kmsCluster,
				KeyName:      c.CryptoKeyName(kmsLocation, kmsKeyRing, kmsKey),
				Pool:         orDefault(kmsPool, "soloz-"+kmsCluster),
				Provider:     orDefault(kmsProvider, "workloads"),
				X509Provider: "control-plane",
			}
			// Desired state is required: a check with nothing to compare against passes.
			if o.Desired, err = kms.ParseExpected(kmsDesiredFrom, kmsCluster); err != nil {
				return err
			}
			// These two are optional BY DESIGN. The carrier may legitimately be unable to
			// reach a cluster, and the report says so rather than quietly passing.
			if kmsJWKSFile != "" {
				if o.LiveJWKS, err = os.ReadFile(kmsJWKSFile); err != nil {
					return fmt.Errorf("reading the cluster JWKS from %s: %w", kmsJWKSFile, err)
				}
			}
			if kmsCAFile != "" {
				if o.ClusterCA, err = os.ReadFile(kmsCAFile); err != nil {
					return fmt.Errorf("reading the cluster CA from %s: %w", kmsCAFile, err)
				}
			}

			report := c.Run(ctx, o)

			if kmsFormat == "json" {
				if err := report.WriteJSON(os.Stdout); err != nil {
					return err
				}
			} else {
				fmt.Printf("%s  %s\n", report.Cluster, report.Key)
				for _, k := range report.Checks {
					fmt.Printf("  %-8s %-14s %s\n", k.Severity, k.Check, k.What)
					if k.Action != "" {
						fmt.Printf("           %s\n", k.Action)
					}
				}
				fmt.Printf("\nworst: %s\n", report.Worst)
			}

			// SilenceUsage: a non-zero exit here is a RESULT, not a misuse of the command,
			// and printing the help text over a security report would bury it.
			cmd.SilenceUsage = true
			switch report.ExitCode() {
			case 1:
				return fmt.Errorf("KMS configuration drift detected; cluster encryption remains " +
					"operational, but desired and authoritative KMS state differ. Manual " +
					"platform-security remediation required")
			case 2:
				return fmt.Errorf("one or more KMS checks could NOT RUN, so the state of this " +
					"cluster's encryption controls is unknown. This is not a passing result: " +
					"treat it as a broken control until it reports again")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kmsCluster, "cluster", "", "cluster to check (required)")
	f.StringVar(&kmsKey, "key", "", "CryptoKey id (default: etcd-kek-<cluster>)")
	f.StringVar(&kmsDesiredFrom, "desired-from",
		"manifests/hub-core-services/crossplane/kms/hub-key.yaml",
		"the reviewed manifest holding desired state and the version floor")
	f.StringVar(&kmsJWKSFile, "jwks-file", "",
		"the cluster's current signing keys. Absent means the staleness check reports UNKNOWN")
	f.StringVar(&kmsCAFile, "ca-file", "",
		"the cluster's CA. Absent means the trust-anchor check reports UNKNOWN")
	f.StringVar(&kmsPool, "pool", "", "workload identity pool (default: soloz-<cluster>)")
	f.StringVar(&kmsProvider, "provider", "", "OIDC provider holding the JWKS (default: workloads)")
	f.StringVar(&kmsFormat, "format", "text", "text or json")
	return cmd
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
