//go:build darwin

package client

import (
	"testing"

	"github.com/go-gl/glfw/v3.3/glfw"
)

func TestWindowMapsBackspaceAndReportsTextOverflow(t *testing.T) {
	if KeyBackspace != KeyLeftAlt+1 || glfwKeys[KeyBackspace] != glfw.KeyBackspace {
		t.Fatalf("Backspace mapping=%d/%v", KeyBackspace, glfwKeys[KeyBackspace])
	}
	var window Window
	for range 1024 {
		window.enqueueTextInput('a')
	}
	window.enqueueTextInput('b')
	got, overflow := window.DrainTextInput(make([]rune, 0, 1024))
	if len(got) != 1024 || !overflow || got[1023] != 'a' {
		t.Fatalf("DrainTextInput len=%d overflow=%v tail=%q", len(got), overflow, got[1023])
	}
	if got, overflow := window.DrainTextInput(got[:0]); len(got) != 0 || overflow {
		t.Fatalf("second drain len=%d overflow=%v", len(got), overflow)
	}
}
