package main

import "testing"

func TestWorkshopID(t *testing.T) {
	for in, want := range map[string]string{
		`{"Name":"x","CreatorUGCIdentity":"3549704863"}`:       "3549704863",
		"\xef\xbb\xbf" + `{"CreatorUGCIdentity":"2890000001"}`: "2890000001",
		`{"CreatorUGCIdentity":3549704863}`:                    "3549704863",
		`{"CreatorUGCIdentity":""}`:                            "",
		`{"CreatorUGCIdentity":"abc123456"}`:                   "",
		`{"Name":"no id"}`:                                     "",
		`not json`:                                             "",
	} {
		if got, _ := workshopID([]byte(in)); got != want {
			t.Errorf("workshopID(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkshopIDName(t *testing.T) {
	ws, name := workshopID([]byte(`{"Name":" Bimba Great Company ","CreatorUGCIdentity":"2538786225"}`))
	if ws != "2538786225" || name != "Bimba Great Company" {
		t.Errorf("got %q %q", ws, name)
	}
}
