package util

import "testing"

func TestGetEnv(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		envValue string
		fallback string
		want     string
		setEnv   bool
	}{
		{
			name:     "trimmed env value wins",
			key:      "LOG_FORGE_TEST_ENV",
			envValue: "  value  ",
			fallback: "fallback",
			want:     "value",
			setEnv:   true,
		},
		{
			name:     "fallback when missing",
			key:      "LOG_FORGE_TEST_ENV_MISSING",
			fallback: "fallback",
			want:     "fallback",
			setEnv:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv(tc.key, tc.envValue)
			}
			if got := GetEnv(tc.key, tc.fallback); got != tc.want {
				t.Fatalf("unexpected value: got=%q want=%q", got, tc.want)
			}
		})
	}
}
