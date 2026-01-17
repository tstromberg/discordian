package github

import (
	"testing"
	"time"
)

// TestTokenEntry_Expiration tests token expiration logic
func TestTokenEntry_Expiration(t *testing.T) {
	tests := []struct {
		name        string
		expiresAt   time.Time
		wantExpired bool
	}{
		{
			name:        "token expired",
			expiresAt:   time.Now().Add(-1 * time.Hour),
			wantExpired: true,
		},
		{
			name:        "token valid for 1 hour",
			expiresAt:   time.Now().Add(1 * time.Hour),
			wantExpired: false,
		},
		{
			name:        "token expiring soon (within buffer)",
			expiresAt:   time.Now().Add(3 * time.Minute),
			wantExpired: true, // within tokenRefreshBuffer
		},
		{
			name:        "token valid beyond buffer",
			expiresAt:   time.Now().Add(10 * time.Minute),
			wantExpired: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &tokenEntry{
				token:     "test-token",
				expiresAt: tt.expiresAt,
			}

			// Check if token is considered expired (needs refresh)
			needsRefresh := time.Until(entry.expiresAt) <= tokenRefreshBuffer

			if needsRefresh != tt.wantExpired {
				t.Errorf("token expiration check = %v, want %v", needsRefresh, tt.wantExpired)
			}
		})
	}
}

// TestAppClient_CacheInitialization tests that caches are properly initialized
func TestAppClient_CacheInitialization(t *testing.T) {
	validKey := `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCmVYzm/VqCi5Vt
rposZRASD5hzAxJnku3XVmxnHUhfO6UjsVph2NIh3/3XkMxM0C2c185d/P4iGtTZ
SAmw0c9E1cGd1sT3G4wH50Bw9+cSNMSnIKFU98KMdMlN2D/HaJnZOKtSnl6yT22/
cx/AzkYBD0NBWeCLQfAmK7Unyg7/vH8U62ZBzJ1pTpEarLQ2WtEUUseRg498/EsX
fEgrL/vydtRJCvIHj3IQhtbSRrd2Ii3QcUhQhtxH4ea2CO3+vVOfAZKQIOL/xF7L
fc0A/osEoEvB9jZogTfHU9xGK7VToTb3nBxR4Sc/ZX9gqCQ5jb5au0i0K31jZ/Tp
I5Hf6KoFAgMBAAECggEAFe7+D4+lGcXSRI5bojMJdXg9AB2Nlb7YQicRUF+aJYS1
+AjxBCoVO4ZP8NcVOaPR//atLdOop1Kmcqh/LqPcExWk3G1vt64YPwqNgtgNzmbK
78brv0qUivTzfqJfdqoib3R7kv9zOUwkCrThoQkSTh13Huz9IR/mzQHCd6a7Z5l6
wgo9JU3B4JXviBjV2CcpYspgsMkUzAbjMIdUBaECg7OfNeBAd0yZVt2HI9+jyn44
gakARkzA8kwQUrPYY5L/BrPDqzS1UShLgFAUaxY5P4wceSWSZcnUT0HvW+yNaJ6C
AUu722Ux5Wjz7TlD31VWbql9KzZd+rLiSUNPdrp0WQKBgQDqjMo4cXC1tAZmngZS
vHAT++BSeFOt16j7QcQV3fm6EALOFVNruNCemLb1IYgmiaIJW0JVGy4dC7tYFDfD
SLumICK1DOiYIepQJGmIHF+E54v4KTMfut82j/5uHpflEtWcaEE9Hl5rCgh1/VDt
jzah/oMB4Xw3ey9iZ+p8shn7lwKBgQC1i7bMTy1Nma3T7Z7MkBqulO3Sb1Y5xTV5
rfNOpuEO2mRMEUB9fkm86U0CDmN01mbQoPr+XgSU1+CR3i7rkolkN8CjmdcsaxrL
CRVur5PRCU9z936OE7TIXhKzmDSvVk3OlVi0c6R3hmLcxVtUCJBofaL7np8ffANX
MhU3t8rqwwKBgEz15WSf1FvKtk71ix2atyvXecOVt99S5B+NdMm4DDkBB+qXFMhD
3DAt69qDJil+/6wSRbGnOXpOXyqHd8ScGPZplPnTQn6oojmpuPbwWGdDkqna2uuO
Za+Bj/qSD0Ua6PxpOP7U+CYnJJ+Sfvt0AnklCdeUJS4PPX0Mm+ROjDgBAoGBAI4e
WXOHaAefjpyhH/czuC+DFsntrqp632np6tZffT+LZ4jE2J9lBYSFfmtlqCYG0WXx
H4uRPjTm6j5GmKSBilyR6JQqEnALSGY5LjX/7M9vYmt+C+xdMODKBAnj1RqNjUtz
ToW1IcMPyMTbGqumKKYj9DrV6etTwam44zNDBe7RAoGBANDbD7/IqqSe91Ip2Ya3
O+mpNiewSI6q/KY4pp6IARwpQPzDWpHlm1/aEncnVpASdekW35VEnuZCW3hbetgo
bxOazQxSjsZ+wfQNMfqsn4uD9qjcGI2oyC/U+FLw7f07X/CrBldF5F6rV3u7OLgP
XPSfScsFYQhv99Qo4yLJceaK
-----END PRIVATE KEY-----`

	client, err := NewAppClient("12345", validKey, nil)
	if err != nil {
		t.Fatalf("NewAppClient() error = %v", err)
	}

	if client.tokens == nil {
		t.Error("NewAppClient() tokens map is nil, want initialized map")
	}

	if client.installations == nil {
		t.Error("NewAppClient() installations map is nil, want initialized map")
	}

	if client.appID != "12345" {
		t.Errorf("NewAppClient() appID = %v, want 12345", client.appID)
	}

	if client.logger == nil {
		t.Error("NewAppClient() logger is nil, want default logger")
	}
}

// TestAppClient_ManualCacheOperations tests manual cache manipulation
func TestAppClient_ManualCacheOperations(t *testing.T) {
	validKey := `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCmVYzm/VqCi5Vt
rposZRASD5hzAxJnku3XVmxnHUhfO6UjsVph2NIh3/3XkMxM0C2c185d/P4iGtTZ
SAmw0c9E1cGd1sT3G4wH50Bw9+cSNMSnIKFU98KMdMlN2D/HaJnZOKtSnl6yT22/
cx/AzkYBD0NBWeCLQfAmK7Unyg7/vH8U62ZBzJ1pTpEarLQ2WtEUUseRg498/EsX
fEgrL/vydtRJCvIHj3IQhtbSRrd2Ii3QcUhQhtxH4ea2CO3+vVOfAZKQIOL/xF7L
fc0A/osEoEvB9jZogTfHU9xGK7VToTb3nBxR4Sc/ZX9gqCQ5jb5au0i0K31jZ/Tp
I5Hf6KoFAgMBAAECggEAFe7+D4+lGcXSRI5bojMJdXg9AB2Nlb7YQicRUF+aJYS1
+AjxBCoVO4ZP8NcVOaPR//atLdOop1Kmcqh/LqPcExWk3G1vt64YPwqNgtgNzmbK
78brv0qUivTzfqJfdqoib3R7kv9zOUwkCrThoQkSTh13Huz9IR/mzQHCd6a7Z5l6
wgo9JU3B4JXviBjV2CcpYspgsMkUzAbjMIdUBaECg7OfNeBAd0yZVt2HI9+jyn44
gakARkzA8kwQUrPYY5L/BrPDqzS1UShLgFAUaxY5P4wceSWSZcnUT0HvW+yNaJ6C
AUu722Ux5Wjz7TlD31VWbql9KzZd+rLiSUNPdrp0WQKBgQDqjMo4cXC1tAZmngZS
vHAT++BSeFOt16j7QcQV3fm6EALOFVNruNCemLb1IYgmiaIJW0JVGy4dC7tYFDfD
SLumICK1DOiYIepQJGmIHF+E54v4KTMfut82j/5uHpflEtWcaEE9Hl5rCgh1/VDt
jzah/oMB4Xw3ey9iZ+p8shn7lwKBgQC1i7bMTy1Nma3T7Z7MkBqulO3Sb1Y5xTV5
rfNOpuEO2mRMEUB9fkm86U0CDmN01mbQoPr+XgSU1+CR3i7rkolkN8CjmdcsaxrL
CRVur5PRCU9z936OE7TIXhKzmDSvVk3OlVi0c6R3hmLcxVtUCJBofaL7np8ffANX
MhU3t8rqwwKBgEz15WSf1FvKtk71ix2atyvXecOVt99S5B+NdMm4DDkBB+qXFMhD
3DAt69qDJil+/6wSRbGnOXpOXyqHd8ScGPZplPnTQn6oojmpuPbwWGdDkqna2uuO
Za+Bj/qSD0Ua6PxpOP7U+CYnJJ+Sfvt0AnklCdeUJS4PPX0Mm+ROjDgBAoGBAI4e
WXOHaAefjpyhH/czuC+DFsntrqp632np6tZffT+LZ4jE2J9lBYSFfmtlqCYG0WXx
H4uRPjTm6j5GmKSBilyR6JQqEnALSGY5LjX/7M9vYmt+C+xdMODKBAnj1RqNjUtz
ToW1IcMPyMTbGqumKKYj9DrV6etTwam44zNDBe7RAoGBANDbD7/IqqSe91Ip2Ya3
O+mpNiewSI6q/KY4pp6IARwpQPzDWpHlm1/aEncnVpASdekW35VEnuZCW3hbetgo
bxOazQxSjsZ+wfQNMfqsn4uD9qjcGI2oyC/U+FLw7f07X/CrBldF5F6rV3u7OLgP
XPSfScsFYQhv99Qo4yLJceaK
-----END PRIVATE KEY-----`

	client, err := NewAppClient("12345", validKey, nil)
	if err != nil {
		t.Fatalf("NewAppClient() error = %v", err)
	}

	t.Run("add installation to cache", func(t *testing.T) {
		client.installations["cached-org"] = int64(999)

		if id, ok := client.installations["cached-org"]; !ok || id != 999 {
			t.Errorf("installations cache[cached-org] = %v, want 999", id)
		}
	})

	t.Run("add token to cache", func(t *testing.T) {
		client.tokens[999] = &tokenEntry{
			token:     "cached-token",
			expiresAt: time.Now().Add(1 * time.Hour),
		}

		if entry, ok := client.tokens[999]; !ok || entry.token != "cached-token" {
			t.Errorf("tokens cache[999] = %v, want cached-token", entry)
		}
	})
}
