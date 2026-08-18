package config

import "testing"

func TestLoadRequiresSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("API_TOKEN", "")
	if _, e := Load(); e == nil {
		t.Fatal("expected missing secret error")
	}
}
func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("API_TOKEN", "token")
	c, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Workers != 4 || c.ListenAddr != ":8080" || c.AllowHTTP || c.AllowPrivate {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}
func TestLoadRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("API_TOKEN", "token")
	t.Setenv("ALLOW_HTTP", "sometimes")
	if _, e := Load(); e == nil {
		t.Fatal("expected invalid boolean error")
	}
}
