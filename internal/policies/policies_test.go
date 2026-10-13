package policies

import "testing"

func TestInheritanceMandatoryAndCycles(t *testing.T) {
	p := System()
	child := p
	child.Metadata.Name = "child"
	child.Spec.Extends = "system"
	child.Spec.Mandatory = false
	child.Spec.Rules = nil
	resolved, err := Resolve(map[string]Policy{"system": p, "child": child})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved["child"].Spec.Mandatory || len(resolved["child"].Spec.Rules) != 1 {
		t.Fatal("mandatory parent removed")
	}
	child.Spec.Extends = "child"
	if _, err = Resolve(map[string]Policy{"child": child}); err == nil {
		t.Fatal("cycle accepted")
	}
	child.Spec.Extends = "missing"
	if _, err = Resolve(map[string]Policy{"child": child}); err == nil {
		t.Fatal("missing parent accepted")
	}
}
