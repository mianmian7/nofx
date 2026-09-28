package mcp

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestProviderAdmissionDefaultsRespectOverridesAndLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, provider, env    string
		explicit, parent, want time.Duration
	}{
		{name: "grok", provider: ProviderGrok, parent: 10 * time.Minute, want: 180 * time.Second},
		{name: "other", provider: "custom", parent: 10 * time.Minute, want: 60 * time.Second},
		{name: "explicit60", provider: ProviderGrok, explicit: 60 * time.Second, parent: 10 * time.Minute, want: 60 * time.Second},
		{name: "environment", provider: ProviderGrok, env: "25", parent: 10 * time.Minute, want: 25 * time.Second},
		{name: "configOverridesEnv", provider: ProviderGrok, env: "25", explicit: 15 * time.Second, parent: 10 * time.Minute, want: 15 * time.Second},
		{name: "parentReserve", provider: ProviderGrok, parent: time.Minute, want: 20 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AI_REQUEST_ADMISSION_TIMEOUT_SECONDS", tc.env)
			synctest.Test(t, func(t *testing.T) {
				cfg := DefaultConfig()
				cfg.CompletionReserve = 30 * time.Second
				cfg.MinAttemptBudget = 10 * time.Second
				if tc.explicit > 0 {
					cfg.AdmissionTimeout = tc.explicit
				}
				c := &Client{Cfg: cfg, Provider: tc.provider}
				parent, cancel := context.WithTimeout(context.Background(), tc.parent)
				defer cancel()
				wait, stop, err := c.admissionContext(parent)
				if err != nil {
					t.Fatal(err)
				}
				defer stop()
				deadline, _ := wait.Deadline()
				if got := time.Until(deadline); got != tc.want {
					t.Fatalf("admission=%v want=%v", got, tc.want)
				}
			})
		})
	}
}

func TestGrokQueueCancellationDoesNotLeakCapacityOrPublishCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scheduler := newModelRequestScheduler(1, 0)
		release, err := scheduler.acquire(context.Background(), "grok")
		if err != nil {
			t.Fatal(err)
		}
		parent, cancel := context.WithCancel(context.Background())
		client := &Client{Provider: ProviderGrok, Cfg: &Config{CompletionReserve: 30 * time.Second, MinAttemptBudget: 10 * time.Second}}
		wait, stop, err := client.admissionContext(parent)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		done := make(chan error, 1)
		go func() {
			r, e := scheduler.acquire(wait, "grok")
			if r != nil {
				r()
			}
			done <- e
		}()
		time.Sleep(10 * time.Second)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
		if left, _ := scheduler.cooldownState("grok"); left != 0 {
			t.Fatal("queue cancellation published cooldown")
		}
		release()
		probe, stopProbe := context.WithTimeout(context.Background(), time.Second)
		defer stopProbe()
		r, err := scheduler.acquire(probe, "grok")
		if err != nil {
			t.Fatal("canceled waiter leaked capacity", err)
		}
		r()
	})
}

func TestGrokAdmissionStillRejectsInsufficientExecutionBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &Client{Provider: ProviderGrok, Cfg: &Config{CompletionReserve: 30 * time.Second, MinAttemptBudget: 10 * time.Second}}
		parent, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if _, stop, err := c.admissionContext(parent); !errors.Is(err, ErrInsufficientRequestBudget) {
			if stop != nil {
				stop()
			}
			t.Fatalf("insufficient budget admitted: %v", err)
		}
	})
}
