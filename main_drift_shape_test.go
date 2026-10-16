package main

import "testing"

func TestTemplateShapeRequiresWholeFileMatch(t *testing.T) {
	tests := []struct {
		name, head, working string
		want                bool
	}{
		{"rendered field", "password: {{PASSWORD}}\n", "password: old-secret\n", true},
		{"empty field", "password: {{PASSWORD}}\n", "password: \n", true},
		{"added setting", "password: {{PASSWORD}}\n", "password: old-secret\nadmin-enabled: true\n", false},
		{"prepended setting", "password: {{PASSWORD}}\n", "admin-enabled: true\npassword: old-secret\n", false},
		{"changed literal", "password: {{PASSWORD}}\nport: 1234\n", "password: old-secret\nport: 5678\n", false},
		{"multiple fields", "user: {{USER}}\npassword: {{PASSWORD}}\n", "user: player\npassword: secret\n", true},
		{"interleaved setting", "user: {{USER}}\npassword: {{PASSWORD}}\n", "user: player\nadmin: true\npassword: secret\n", false},
		{"literal suffix", "value={{VALUE}};end", "value=old;end trailing", false},
		{"regex literal", "[value]={{VALUE}}\n", "[value]=old\n", true},
		{"no placeholder", "value=old\n", "value=old\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateShapeMatches(tt.head, tt.working); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
