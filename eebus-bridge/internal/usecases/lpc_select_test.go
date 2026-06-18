package usecases

import "testing"

func TestLPCEntityMatchesSKI(t *testing.T) {
	tests := []struct {
		name       string
		entitySKI  string
		want       string
		wantResult bool
	}{
		{"empty want matches any", "abcd1234", "", true},
		{"exact match", "abcd1234", "abcd1234", true},
		{"case insensitive", "ABCD1234", "abcd1234", true},
		{"whitespace insensitive", "ab cd 12 34", "abcd1234", true},
		{"surrounding whitespace", "  abcd1234  ", "abcd1234", true},
		{"mismatch", "abcd1234", "ffff0000", false},
		{"empty entity ski non-empty want", "", "abcd1234", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lpcEntityMatchesSKI(tt.entitySKI, tt.want); got != tt.wantResult {
				t.Errorf("lpcEntityMatchesSKI(%q, %q) = %v, want %v",
					tt.entitySKI, tt.want, got, tt.wantResult)
			}
		})
	}
}

// CompatibleEntity must not panic and must return nil before Setup (uc == nil),
// so resolveEntity falls back to the registry instead of crashing.
func TestCompatibleEntityNilUseCase(t *testing.T) {
	w := NewLPCWrapper(nil, nil, false)
	if got := w.CompatibleEntity("abcd1234"); got != nil {
		t.Errorf("CompatibleEntity on un-set-up wrapper = %v, want nil", got)
	}
}
