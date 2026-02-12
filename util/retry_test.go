package util

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestWaitForRetry(t *testing.T) {
	cases := []struct {
		name     string
		buildCtx func() context.Context
		delay    time.Duration
		want     bool
	}{
		{
			name: "non-positive delay with active context returns true",
			buildCtx: func() context.Context {
				return context.Background()
			},
			delay: 0,
			want:  true,
		},
		{
			name: "non-positive delay with canceled context returns false",
			buildCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			delay: 0,
			want:  false,
		},
		{
			name: "positive delay waits and returns true",
			buildCtx: func() context.Context {
				return context.Background()
			},
			delay: 5 * time.Millisecond,
			want:  true,
		},
		{
			name: "canceled context returns false",
			buildCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			delay: 50 * time.Millisecond,
			want:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := WaitForRetry(tc.buildCtx(), tc.delay); got != tc.want {
				t.Fatalf("unexpected result: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestJitterRetryDelay(t *testing.T) {
	cases := []struct {
		name string
		base time.Duration
		rng  *rand.Rand
	}{
		{
			name: "non-positive base remains unchanged",
			base: 0,
			rng:  rand.New(rand.NewSource(1)),
		},
		{
			name: "nil rand keeps base",
			base: time.Second,
			rng:  nil,
		},
		{
			name: "positive base stays within jitter bounds",
			base: time.Second,
			rng:  rand.New(rand.NewSource(1)),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := JitterRetryDelay(tc.base, tc.rng)
			if tc.base <= 0 || tc.rng == nil {
				if got != tc.base {
					t.Fatalf("unexpected unchanged delay: got=%v want=%v", got, tc.base)
				}
				return
			}
			jitter := tc.base / 5
			minDelay := tc.base - jitter
			maxDelay := tc.base + jitter
			if got < minDelay || got > maxDelay {
				t.Fatalf("delay out of jitter range: got=%v range=[%v,%v]", got, minDelay, maxDelay)
			}
		})
	}
}

func TestNextRetryDelay(t *testing.T) {
	cases := []struct {
		name    string
		current time.Duration
		min     time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{
			name:    "non-positive current returns min",
			current: 0,
			min:     100 * time.Millisecond,
			max:     time.Second,
			want:    100 * time.Millisecond,
		},
		{
			name:    "doubles current within max",
			current: 200 * time.Millisecond,
			min:     100 * time.Millisecond,
			max:     time.Second,
			want:    400 * time.Millisecond,
		},
		{
			name:    "caps at max",
			current: 600 * time.Millisecond,
			min:     100 * time.Millisecond,
			max:     time.Second,
			want:    time.Second,
		},
		{
			name:    "invalid min falls back to one millisecond",
			current: 0,
			min:     0,
			max:     time.Second,
			want:    time.Millisecond,
		},
		{
			name:    "max below min coerces to min",
			current: 200 * time.Millisecond,
			min:     300 * time.Millisecond,
			max:     200 * time.Millisecond,
			want:    300 * time.Millisecond,
		},
		{
			name:    "overflow caps at max",
			current: time.Duration(math.MaxInt64 / 2),
			min:     time.Millisecond,
			max:     5 * time.Second,
			want:    5 * time.Second,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := NextRetryDelay(tc.current, tc.min, tc.max); got != tc.want {
				t.Fatalf("unexpected next delay: got=%v want=%v", got, tc.want)
			}
		})
	}
}
