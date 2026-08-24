package main

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// recvTunFD listens on the unix socket sockPath and receives exactly one
// file descriptor, passed by the Android side through SCM_RIGHTS (see
// LocalSocket.setFileDescriptorsForSend in TunFdBridge.kt). Needed because
// go_client is a separate OS process (not JNI/in-process), so the TUN that
// Android creates through VpnService.Builder().establish() cannot be handed
// over except by passing the descriptor between processes.
//
// go_client is the server (it listens) and Android is the client (it
// connects after establish()), not the other way round. It used to be
// reversed (Android listened, go_client dialled with retries) — that
// created a race: go_client starts before Android can bring the TUN up and
// create the LocalServerSocket, so the first attempt almost always got
// connection refused. Inverting it removes the race entirely — go_client is
// already listening long before Android even begins establish().
func recvTunFD(sockPath string) (*os.File, error) {
	rawDiagf("recvTunFD: listen unix %q", sockPath)
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		rawDiagf("recvTunFD: ResolveUnixAddr FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock resolve: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		rawDiagf("recvTunFD: ListenUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock listen: %w", err)
	}
	defer ln.Close()
	rawDiagf("recvTunFD: listening, waiting for Android to connect...")

	uc, err := ln.AcceptUnix()
	if err != nil {
		rawDiagf("recvTunFD: AcceptUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock accept: %w", err)
	}
	defer uc.Close()
	rawDiagf("recvTunFD: accept OK, waiting for SCM_RIGHTS...")

	buf := make([]byte, 4)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		rawDiagf("recvTunFD: ReadMsgUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock read: %w", err)
	}
	rawDiagf("recvTunFD: ReadMsgUnix OK, oobn=%d", oobn)

	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		rawDiagf("recvTunFD: ParseSocketControlMessage FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock parse cmsg: %w", err)
	}
	if len(scms) == 0 {
		rawDiagf("recvTunFD: 0 control messages received")
		return nil, fmt.Errorf("tun-fd-sock: no control message received")
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		rawDiagf("recvTunFD: ParseUnixRights FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock parse rights: %w", err)
	}
	if len(fds) == 0 {
		rawDiagf("recvTunFD: 0 fds in rights message")
		return nil, fmt.Errorf("tun-fd-sock: no fd received")
	}

	rawDiagf("recvTunFD: received fd=%d", fds[0])
	return os.NewFile(uintptr(fds[0]), "tun"), nil
}
