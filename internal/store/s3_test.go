package store

import (
	"testing"

	"github.com/dimab0nd/dumper/internal/config"
)

func TestCredentialsFromConfig(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "from-env")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "from-env-secret")
	keyID, secret, err := credentialsFrom(config.S3Config{
		AccessKeyID:     "from-cfg",
		SecretAccessKey: "from-cfg-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "from-cfg" || secret != "from-cfg-secret" {
		t.Fatalf("credentials = %q %q", keyID, secret)
	}
}

func TestCredentialsFromEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	keyID, secret, err := credentialsFrom(config.S3Config{})
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "id" || secret != "secret" {
		t.Fatalf("credentials = %q %q", keyID, secret)
	}
}

func TestCredentialsMissing(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	if _, _, err := credentialsFrom(config.S3Config{}); err == nil {
		t.Fatal("expected missing credentials error")
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	_, err := New(config.S3Config{Region: "us-east-1", Bucket: "backups"})
	if err == nil {
		t.Fatal("expected error")
	}
}
