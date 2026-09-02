package controller

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
)

var (
	userGroupsReconcileTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_operator_usergroups_reconcile_total",
		Help: "Total number of user groups reconciliations",
	})
	userGroupsSyncTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hub_operator_usergroups_sync_total",
		Help: "Total number of user group syncs",
	}, []string{"email", "status"})
	userGroupsLastReconcile = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hub_operator_usergroups_last_reconcile_timestamp",
		Help: "Timestamp of last successful user groups reconciliation",
	})
)

func init() {
	metrics.Registry.MustRegister(userGroupsReconcileTotal, userGroupsSyncTotal, userGroupsLastReconcile)
}

// UserGroupsReconciler watches the identity-user-groups ConfigMap and
// reconciles Kratos metadata_public.groups to match the desired state.
// Git/ArgoCD owns desired group configuration, this controller owns
// continuous reconciliation, Kratos owns identity state, and the
// control-plane bootstrap sync provides recovery.
type UserGroupsReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Auth          interfaces.IAuth
	ConfigMapName string
	ConfigMapNS   string
	DataKey       string
}

// userGroupsConfig is the structure of the groups.yaml data in the ConfigMap.
type userGroupsConfig struct {
	Users []struct {
		Email  string   `yaml:"email"`
		Groups []string `yaml:"groups"`
	} `yaml:"users"`
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *UserGroupsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	userGroupsReconcileTotal.Inc()

	// Fetch the ConfigMap
	var cm corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &cm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	data, ok := cm.Data[r.DataKey]
	if !ok {
		log.Info("Key not found in ConfigMap, skipping", "key", r.DataKey)
		return ctrl.Result{}, nil
	}

	var cfg userGroupsConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		log.Error(err, "Failed to parse groups.yaml")
		return ctrl.Result{}, fmt.Errorf("parse groups.yaml: %w", err)
	}

	// Build desired state: email -> groups
	desired := make(map[string][]string, len(cfg.Users))
	for _, u := range cfg.Users {
		desired[u.Email] = u.Groups
	}

	// List all users to find stale group assignments (deletion semantics)
	users, err := r.Auth.ListUsers(ctx, "", "", "")
	if err != nil {
		log.Error(err, "Failed to list users")
		return ctrl.Result{}, fmt.Errorf("list users: %w", err)
	}

	var failures []string

	// Sync groups for users in ConfigMap
	for email, groups := range desired {
		if err := r.Auth.SetUserGroups(ctx, email, groups); err != nil {
			log.Error(err, "Failed to set groups", "email", email)
			userGroupsSyncTotal.WithLabelValues(email, "failure").Inc()
			failures = append(failures, email)
			continue
		}
		log.Info("Synced groups", "email", email, "groups", groups)
		userGroupsSyncTotal.WithLabelValues(email, "success").Inc()
	}

	// Remove groups for users NOT in ConfigMap (deletion semantics)
	for _, u := range users {
		if _, ok := desired[u.Email]; !ok && len(u.Groups) > 0 {
			empty := []string{}
			if err := r.Auth.SetUserGroups(ctx, u.Email, empty); err != nil {
				log.Error(err, "Failed to remove groups", "email", u.Email)
				userGroupsSyncTotal.WithLabelValues(u.Email, "failure").Inc()
				failures = append(failures, u.Email)
				continue
			}
			log.Info("Removed stale groups", "email", u.Email)
			userGroupsSyncTotal.WithLabelValues(u.Email, "success").Inc()
		}
	}

	if len(failures) > 0 {
		return ctrl.Result{}, fmt.Errorf("failed to sync groups for: %v", failures)
	}

	userGroupsLastReconcile.SetToCurrentTime()
	return ctrl.Result{}, nil
}

func (r *UserGroupsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.findUserGroupsConfigMap),
			builder.WithPredicates(
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == r.ConfigMapName &&
						obj.GetNamespace() == r.ConfigMapNS
				}),
			),
		).
		Complete(r)
}

func (r *UserGroupsReconciler) findUserGroupsConfigMap(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{
		{NamespacedName: client.ObjectKeyFromObject(obj)},
	}
}

// groupsEqual compares two string slices for equality (order-independent).
func groupsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSorted := make([]string, len(a))
	bSorted := make([]string, len(b))
	copy(aSorted, a)
	copy(bSorted, b)
	sort.Strings(aSorted)
	sort.Strings(bSorted)
	return slices.Equal(aSorted, bSorted)
}
