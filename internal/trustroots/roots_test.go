package trustroots

import (
	"crypto/x509"
	"testing"
)

func TestOnlyEmbeddedRoots(t *testing.T) {
	only := x509.NewCertPool()
	if !only.AppendCertsFromPEM(mozillaPEM) {
		t.Fatal("embedded bundle has no certificates")
	}
	if got, want := len(Roots().Subjects()), len(only.Subjects()); got != want {
		t.Fatalf("trust pool = %d subjects, embedded bundle = %d", got, want)
	}
	if len(only.Subjects()) < 100 {
		t.Fatalf("embedded public roots missing, got %d", len(only.Subjects()))
	}
}
