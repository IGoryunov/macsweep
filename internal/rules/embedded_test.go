package rules_test

import (
	"testing"

	"github.com/IGoryunov/macsweep/internal/rules"
	rulesfs "github.com/IGoryunov/macsweep/rules"
)

func TestEmbeddedRulesValid(t *testing.T) {
	set, err := rules.Load(rulesfs.FS())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Rules) < 60 || len(set.Groups) != 10 {
		t.Fatalf("unexpected embedded set: %d rules, groups %v", len(set.Rules), set.Groups)
	}
	for _, r := range set.Rules {
		if r.Note == "" || r.Recovery == "" {
			t.Errorf("rule %s lacks note or recovery", r.ID)
		}
	}
	if rules.Hash(rulesfs.FS()) == "" {
		t.Fatal("empty hash")
	}
}
