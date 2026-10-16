package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestRejectReservedFileDestinations(t *testing.T) {
	for _, path := range []string{".", "./", ".tidy", "./.tidy/state.json", ".git/config"} {
		_, err := ParseConfig([]byte(fmt.Sprintf("[server]\nproject='paper'\nversion='1.21.1'\n[worlds.test]\npath=%q\nurl='https://example.com/world.zip'\nsha256='%s'\nextract=true\nonce=false\n", path, strings.Repeat("a", 64))))
		if err == nil {
			t.Errorf("accepted reserved path %q", path)
		}
	}
}
