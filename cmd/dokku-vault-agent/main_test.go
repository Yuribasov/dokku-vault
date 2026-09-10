package main

import (
	"reflect"
	"testing"
)

func TestCommandInvocationRoutesUnknownDefaultCommand(t *testing.T) {
	action, args := commandInvocation("default", []string{"vault-agent:stag", "sample"})
	if action != "stag" || !reflect.DeepEqual(args, []string{"sample"}) {
		t.Fatalf("commandInvocation() = %q, %#v; want stag, [sample]", action, args)
	}
}

func TestCommandInvocationPreservesNamedCommand(t *testing.T) {
	action, args := commandInvocation("stage", []string{"vault-agent:stage", "sample"})
	if action != "stage" || !reflect.DeepEqual(args, []string{"sample"}) {
		t.Fatalf("commandInvocation() = %q, %#v; want stage, [sample]", action, args)
	}
}
