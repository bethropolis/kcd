package ipc

import (
	"testing"
)

func TestPromptManagerRejectsUndeclaredChoice(t *testing.T) {
	manager := NewPromptManager(nil)
	manager.pending["p1"] = &promptWaiter{prompt: Prompt{PromptID: "p1", Options: []string{"yes", "no"}}, choice: make(chan string, 1)}
	if manager.Respond("p1", "maybe") {
		t.Fatal("undeclared prompt choice was accepted")
	}
	if !manager.Respond("p1", "yes") {
		t.Fatal("declared prompt choice was rejected")
	}
}
