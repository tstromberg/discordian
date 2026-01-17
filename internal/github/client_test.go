package github

import (
	"testing"
)

// TestNewAppClient tests AppClient construction.
func TestNewAppClient(t *testing.T) {
	t.Run("invalid private key", func(t *testing.T) {
		_, err := NewAppClient("12345", "invalid-key", nil)
		if err == nil {
			t.Error("NewAppClient() error = nil, want error for invalid key")
		}
	})

	t.Run("empty private key", func(t *testing.T) {
		_, err := NewAppClient("12345", "", nil)
		if err == nil {
			t.Error("NewAppClient() error = nil, want error for empty key")
		}
	})

	t.Run("valid RSA key", func(t *testing.T) {
		// Valid 2048-bit RSA private key in PKCS8 format for testing
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
			t.Errorf("NewAppClient() error = %v, want nil", err)
		}
		if client == nil {
			t.Fatal("NewAppClient() returned nil client")
		}
		if client.appID != "12345" {
			t.Errorf("NewAppClient() appID = %s, want 12345", client.appID)
		}
		if client.logger == nil {
			t.Error("NewAppClient() logger should default to slog.Default()")
		}
		if client.tokens == nil {
			t.Error("NewAppClient() tokens map should be initialized")
		}
		if client.installations == nil {
			t.Error("NewAppClient() installations map should be initialized")
		}
	})
}
