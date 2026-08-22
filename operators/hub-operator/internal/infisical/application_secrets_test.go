package infisical

import (
	"encoding/hex"
	"testing"
)

// agentgateway hex-decodes OIDC_COOKIE_SECRET and requires exactly 32 bytes for
// AES-256-GCM. A value from the default URL-safe charset is the right LENGTH but
// the wrong alphabet, and the failure surfaces only at gateway startup as
// "Invalid character 'G' at position 0" — which names neither hex nor the key.
func TestHexKeyConsumersDeclareTheirByteLength(t *testing.T) {
	want := map[string]int{
		"agentgateway-oidc-cookie-secret": 32,
	}
	for key, bytes := range want {
		found := false
		for _, d := range ApplicationSecretMappings {
			if d.PasswordKey != key {
				continue
			}
			found = true
			if d.HexBytes != bytes {
				t.Errorf("%s: HexBytes=%d, want %d — a non-hex value fails to parse at startup",
					key, d.HexBytes, bytes)
			}
		}
		if !found {
			t.Errorf("%s missing from ApplicationSecretMappings — nothing would produce it", key)
		}
	}
}

// Two producers for one key means last writer wins, silently.
func TestNoDuplicateInfisicalKeys(t *testing.T) {
	seen := map[string]string{}
	claim := func(key, owner string) {
		if key == "" {
			return
		}
		if prev, ok := seen[key]; ok {
			t.Errorf("Infisical key %q produced by both %q and %q", key, prev, owner)
		}
		seen[key] = owner
	}
	for _, d := range ApplicationSecretMappings {
		claim(d.UsernameKey, "ApplicationSecretMappings:"+d.Description)
		claim(d.PasswordKey, "ApplicationSecretMappings:"+d.Description)
	}
	for _, m := range CLISecretMappings {
		claim(m.InfisicalKey, "CLISecretMappings:"+m.Description)
	}
}

// Guards the generator itself: right length, and actually decodable as hex.
func TestGeneratedHexKeysDecodeToRequestedLength(t *testing.T) {
	for _, d := range ApplicationSecretMappings {
		if d.HexBytes == 0 {
			continue
		}
		raw, err := hex.DecodeString(mustHex(t, d.HexBytes))
		if err != nil {
			t.Fatalf("%s: generated key is not valid hex: %v", d.PasswordKey, err)
		}
		if len(raw) != d.HexBytes {
			t.Errorf("%s: decoded to %d bytes, want %d", d.PasswordKey, len(raw), d.HexBytes)
		}
	}
}
