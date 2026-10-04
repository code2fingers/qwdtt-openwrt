package main

import "testing"

// Two relays pointed at one local port do not collide loudly. SO_REUSEADDR
// lets the second socket bind an address the first already holds, so nothing
// fails: the packets are split between them and the WireGuard interface above
// quietly stops working. That is why the protocol handler refuses the
// configuration (DUPLICATE_LISTEN_PORT) rather than leaving it to be found,
// and why the client cannot detect it for itself.
//
// Asserted rather than assumed: if a kernel or a socket option ever makes the
// second bind fail instead, the refusal rests on a different failure and the
// reasoning beside it needs rereading.
func TestListenUDPLetsASecondSocketShareThePort(t *testing.T) {
	first, err := listenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	defer first.Close()

	addr := first.LocalAddr().String()
	second, err := listenUDP(addr)
	if err != nil {
		t.Fatalf("second bind on %s was refused (%v); the handler's duplicate-port refusal now guards a different failure", addr, err)
	}
	_ = second.Close()
}
