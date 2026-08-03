package credentials

import "testing"

func TestVaultUsesPrimaryKeyAndCanReadFallbackCiphertext(t *testing.T) {
	legacy := New("legacy-api-key")
	oldCiphertext, err := legacy.Seal("alice", "old-password")
	if err != nil {
		t.Fatal(err)
	}
	vault := New("new-credential-master-key", "legacy-api-key")
	password, err := vault.Open("alice", oldCiphertext)
	if err != nil {
		t.Fatalf("open legacy ciphertext: %v", err)
	}
	if password != "old-password" {
		t.Fatalf("legacy password = %q", password)
	}

	ciphertext, err := vault.Seal("alice", "new-password")
	if err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) < 3 || ciphertext[:3] != "v2:" {
		t.Fatalf("ciphertext = %q, want v2 prefix", ciphertext)
	}
	if _, err := legacy.Open("alice", ciphertext); err == nil {
		t.Fatal("legacy key must not decrypt ciphertext sealed by primary key")
	}
	password, err = vault.Open("alice", ciphertext)
	if err != nil || password != "new-password" {
		t.Fatalf("open new ciphertext = %q, %v", password, err)
	}
}
