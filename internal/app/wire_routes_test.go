package app

import (
	"slices"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
)

// TestWorkerPin: the workers routes list are kept in order with their
// models, and the pin waits only when a route waits and nothing after the
// workers is open to the pages.
func TestWorkerPin(t *testing.T) {
	routes := []model.UpscaleRoute{
		{Target: 5, Model: "big"},
		{Target: 9, Wait: true},
		{Target: 5, Model: "other"}, // listed again: the first route's model stays
	}
	pin := workerPin(routes, false)
	if !slices.Equal(pin.Workers, []int64{5, 9}) || pin.Models[5] != "big" || pin.Models[9] != "" || !pin.Strict {
		t.Fatalf("pin %+v", pin)
	}
	if pin := workerPin(routes, true); pin.Strict {
		t.Fatal("a pin with an upscaler open after its workers waits for them")
	}
	if pin := workerPin(routes[:1], false); pin.Strict {
		t.Fatal("a pin waits though no route does")
	}
}
