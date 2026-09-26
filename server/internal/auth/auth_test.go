package auth

import (
	"fmt"
	"testing"
)

func TestLoginGuard(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	g := NewLoginGuard()

	if !g.Check("1.1.1.1", "alice", hash, "correct horse") {
		t.Fatal("right password rejected")
	}
	if g.Check("1.1.1.1", "nobody", "", "correct horse") {
		t.Fatal("unknown user accepted")
	}

	// One IP guessing one account is stopped after 10 failures.
	for i := 0; i < 10; i++ {
		g.Check("2.2.2.2", "alice", hash, "wrong")
	}
	if g.Allowed("2.2.2.2", "alice") {
		t.Fatal("pair limit not applied")
	}
	if !g.Allowed("3.3.3.3", "alice") {
		t.Fatal("other IP blocked too early")
	}

	// Many IPs guessing one account hit the account limit.
	for i := 0; i < 30; i++ {
		g.Check(fmt.Sprintf("10.0.%d.1", i), "Alice", hash, "wrong")
	}
	if g.Allowed("9.9.9.9", "alice") {
		t.Fatal("account limit not applied across IPs")
	}

	// One IP trying many names hits the IP limit.
	for i := 0; i < 30; i++ {
		g.Check("4.4.4.4", fmt.Sprintf("user%d", i), "", "wrong")
	}
	if g.Allowed("4.4.4.4", "someone-else") {
		t.Fatal("IP limit not applied")
	}
	// Failures for unknown names do not lock out an account of that name.
	if !g.Allowed("5.5.5.5", "user1") {
		t.Fatal("unknown names counted against accounts")
	}
}
