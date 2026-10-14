package dispatch

import (
	"context"
	"reflect"
	"testing"
)

// Every map in the state New and newRunState set up is made there, once: nothing makes one as it
// first writes to it, so a map left out would be a write to a nil map, a panic mid-run.
func TestOwnersMakeTheirMaps(t *testing.T) {
	t.Parallel()
	o := New(Config{}, nil, "", Deps{})
	for _, c := range []struct {
		owner string
		state any
	}{
		{"ledger", &o.ledger},
		{"tallies", &o.tallies},
		{"predictor", &o.predictor},
		{"runState", newRunState(context.Background(), o)},
	} {
		v := reflect.ValueOf(c.state).Elem()
		for i := range v.NumField() {
			if f := v.Field(i); f.Kind() == reflect.Map && f.IsNil() {
				t.Errorf("%s.%s is nil", c.owner, v.Type().Field(i).Name)
			}
		}
	}
}
