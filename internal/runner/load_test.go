package runner

import "testing"

func TestTheLoadAverageIsZeroOrMore(t *testing.T) {
	load, err := LoadAverage()
	if err != nil || load < 0 {
		t.Errorf("load = %v, %v", load, err)
	}
}
