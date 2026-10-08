package access

import "testing"

// TestBundles: "Manage the library" includes the library permissions,
// managing the queue includes seeing it, and the parts stay apart.
func TestBundles(t *testing.T) {
	manager := &Principal{Perms: map[string]bool{LibraryManage: true}}
	for _, p := range []string{LibraryAdd, LibraryEdit, LibraryDelete, QueueManage, ActivityView} {
		if !manager.Can(p) {
			t.Errorf("library.manage lacks %s", p)
		}
	}
	if manager.Can(Admin) || manager.Can(Apps) {
		t.Error("library.manage grants more than the library")
	}
	adder := &Principal{Perms: map[string]bool{LibraryAdd: true}}
	if adder.Can(LibraryEdit) || adder.Can(LibraryDelete) || adder.Can(LibraryManage) || adder.Can(QueueManage) {
		t.Error("adding titles grants more than adding")
	}
	queue := &Principal{Perms: map[string]bool{QueueManage: true}}
	if !queue.Can(ActivityView) || queue.Can(LibraryEdit) {
		t.Error("queue.manage should include activity.view only")
	}
}

// TestContentLimits: a group's highest rating and hidden genres hide titles.
func TestContentLimits(t *testing.T) {
	if RatingOf("Adults Only 18+", false, nil) != RatingAdult || RatingOf("", false, []string{"Ecchi"}) != RatingMature || RatingOf("", false, []string{"Action"}) != RatingAll {
		t.Fatal("ratings")
	}
	teen := Scope{MaxRating: "teen", BlockedGenres: []string{"gore"}}
	for _, c := range []struct {
		rating string
		genres []string
		ok     bool
	}{
		{"", []string{"Action"}, true},
		{"Teen", nil, true},
		{"", []string{"Ecchi"}, false},
		{"Adults Only 18+", nil, false},
		{"", []string{"Gore"}, false},
	} {
		if got := teen.AllowsContent(c.rating, false, c.genres); got != c.ok {
			t.Errorf("%q %v: %v", c.rating, c.genres, got)
		}
	}
	if !(Scope{}).AllowsContent("Adults Only 18+", true, []string{"Hentai"}) {
		t.Error("no limit hides something")
	}
	if !teen.Limited() || (Scope{}).Limited() {
		t.Error("Limited")
	}
}
