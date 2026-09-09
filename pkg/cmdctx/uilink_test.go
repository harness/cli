// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package cmdctx

import "testing"

func TestPushPopUILink_RoundTrip(t *testing.T) {
	c := &Ctx{}
	c.PushUILink(UILink{Verb: "get", Noun: "pr", Id: "1"})

	link, ok := c.PopUILink()
	if !ok {
		t.Fatal("PopUILink: ok = false, want true")
	}
	if link.Verb != "get" || link.Noun != "pr" || link.Id != "1" {
		t.Fatalf("PopUILink = %+v, want Verb=get Noun=pr Id=1", link)
	}
	if len(c.UIHistory) != 0 {
		t.Fatalf("UIHistory len = %d, want 0 after pop", len(c.UIHistory))
	}
}

func TestPopUILink_Empty(t *testing.T) {
	c := &Ctx{}
	link, ok := c.PopUILink()
	if ok {
		t.Fatal("PopUILink on empty stack: ok = true, want false")
	}
	if link.Verb != "" || link.Noun != "" || link.Id != "" {
		t.Fatalf("PopUILink on empty stack: link = %+v, want zero value", link)
	}
}

func TestPushPopUILink_LIFOOrder(t *testing.T) {
	c := &Ctx{}
	c.PushUILink(UILink{Noun: "first"})
	c.PushUILink(UILink{Noun: "second"})
	c.PushUILink(UILink{Noun: "third"})

	want := []string{"third", "second", "first"}
	for _, w := range want {
		link, ok := c.PopUILink()
		if !ok {
			t.Fatalf("PopUILink: ok = false, want true (expected %q)", w)
		}
		if link.Noun != w {
			t.Fatalf("PopUILink.Noun = %q, want %q", link.Noun, w)
		}
	}
	if _, ok := c.PopUILink(); ok {
		t.Fatal("PopUILink after draining stack: ok = true, want false")
	}
}
