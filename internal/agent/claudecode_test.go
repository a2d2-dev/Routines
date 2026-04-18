/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agent

import (
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		payload string
		want    string
	}{
		{
			name:    "empty payload",
			prompt:  "Do the thing",
			payload: "",
			want:    "Do the thing",
		},
		{
			name:    "empty JSON object payload",
			prompt:  "Do the thing",
			payload: "{}",
			want:    "Do the thing",
		},
		{
			name:    "non-empty payload",
			prompt:  "Do the thing",
			payload: `{"event":"push"}`,
			want:    "Do the thing\n\n---INPUT---\n{\"event\":\"push\"}",
		},
		{
			name:    "whitespace-only payload",
			prompt:  "Do the thing",
			payload: "   ",
			want:    "Do the thing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrompt(tc.prompt, tc.payload)
			if got != tc.want {
				t.Errorf("buildPrompt() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTokensFromOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{
			name:  "empty output",
			input: "",
			want:  0,
		},
		{
			name:  "JSON with usage",
			input: `{"usage":{"input_tokens":100,"output_tokens":50}}`,
			want:  150,
		},
		{
			name:  "JSON with total_tokens regexp fallback",
			input: `some text "total_tokens": 200 more text`,
			want:  200,
		},
		{
			name:  "multiple JSON lines, picks highest",
			input: "ignored\n{\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n{\"usage\":{\"input_tokens\":200,\"output_tokens\":300}}",
			want:  500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTokensFromOutput([]byte(tc.input))
			if got != tc.want {
				t.Errorf("parseTokensFromOutput() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseSessionID(t *testing.T) {
	uuid := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{
			name:   "no session ID",
			stdout: "some output",
			stderr: "some error",
			want:   "",
		},
		{
			name:   "session ID in stdout with 'session' keyword",
			stdout: "Session: " + uuid,
			stderr: "",
			want:   uuid,
		},
		{
			name:   "session ID in stderr fallback",
			stdout: "",
			stderr: "Starting session " + uuid,
			want:   uuid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSessionID([]byte(tc.stdout), []byte(tc.stderr))
			if got != tc.want {
				t.Errorf("parseSessionID() = %q, want %q", got, tc.want)
			}
		})
	}
}
