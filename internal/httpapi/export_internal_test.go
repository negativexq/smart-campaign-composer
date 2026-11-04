package httpapi

import "testing"

func TestCustomerTextCannotRunAsAFormula(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"=1+1": "'=1+1", "+90": "'+90", "-2": "'-2", "@x": "'@x", "\tx": "'\tx", "Ayşe": "Ayşe", "": "",
	} {
		if got := safeCell(text); got != want {
			t.Errorf("%q became %q", text, got)
		}
	}
}
