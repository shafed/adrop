package pairing

import "testing"

func TestCodeIsSymmetricAndBindsBothFingerprints(t *testing.T) {
	a, b, m := "aa11", "bb22", "cc33"
	if Code(a, b) != Code(b, a) {
		t.Fatal("code depends on the order of the fingerprints")
	}
	if len(Code(a, b)) != 6 {
		t.Fatalf("code %q is not 6 digits", Code(a, b))
	}
	// A man in the middle shows his own certificate to each side.
	if Code(a, m) == Code(a, b) || Code(m, b) == Code(a, b) {
		t.Fatal("a substituted certificate kept the code")
	}
}
