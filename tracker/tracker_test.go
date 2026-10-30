package tracker

import "testing"

func TestAppleScriptString(t *testing.T) {
	got := appleScriptString(`Tornado "Warning" C:\path ☂`)
	want := `"Tornado \"Warning\" C:\\path ☂"`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
