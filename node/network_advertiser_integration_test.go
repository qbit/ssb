// +build integration

package node

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.cryptoscope.co/ssb"
)

// To ensure this works, add this to the SUDO file, where USER is your username.
//
// Defaults env_keep += "SEND_ADV RECV_ADV"
// USER ALL=(ALL) NOPASSWD: /sbin/ip
// USER ALL=(ALL) NOPASSWD: ! /sbin/ip netns exec *
// USER ALL=(ALL) NOPASSWD: /sbin/ip netns exec local ip *
// USER ALL=(ALL) NOPASSWD: /sbin/ip netns exec local sudo -u USER -- *
// USER ALL=(ALL) NOPASSWD: /sbin/ip netns exec peer ip *
// USER ALL=(ALL) NOPASSWD: /sbin/ip netns exec peer sudo -u USER -- *
// USER ALL=(ALL) NOPASSWD: /sbin/ip netns exec testNet sudo -u USER -- *
// USER ALL=(ALL) NOPASSWD: /usr/sbin/brctl

func sudo(args ...string) *exec.Cmd {
	ret := exec.Command("sudo", args...)
	ret.Stderr = os.Stdout
	return ret
}

type NetConfig struct {
	Address   string
	Gateway   string
	Broadcast string
}

func createTestNetwork(name string, gateway NetConfig) (func(), error) {
	err := sudo("brctl", "addbr", name).Run()
	if err != nil {
		return nil, err
	}
	err = sudo("brctl", "stp", name, "off").Run()
	if err != nil {
		return nil, errors.Wrap(err, "stp")
	}
	err = sudo("ip", "link", "set", "dev", name, "up").Run()
	if err != nil {
		return nil, errors.Wrap(err, "setting dev up")
	}

	// Set gateway address
	if gateway.Address != "" {
		err = sudo("ip", "addr", "add", gateway.Address, "dev", name).Run()
		if err != nil {
			return nil, errors.Wrap(err, "setting gateway address")
		}
	}

	removeBridge := func() {
		_ = sudo("ip", "link", "set", "dev", name, "down").Run()
		_ = sudo("brctl", "delbr", name).Run()
	}
	return removeBridge, nil
}

func addNode(nodeName string, netName string, addresses ...NetConfig) (func(), error) {
	vethNode := nodeName
	vethHost := "br-" + nodeName
	err := sudo("ip", "link", "add",
		vethNode, "type", "veth", "peer", "name", vethHost,
	).Run()
	if err != nil {
		return nil, errors.Wrap(err, "adding veth "+vethHost)
	}

	bridgeName := netName
	netns := nodeName
	rmNode := func() {
		_ = sudo("ip", "link", "del", vethHost).Run()
		_ = sudo("ip", "netns", "del", netns).Run()
		_ = sudo("brctl", "delif", bridgeName).Run()
	}

	// Add to test net using bridge
	err = sudo("brctl", "addif", bridgeName, vethHost).Run()
	if err != nil {
		return rmNode, errors.Wrap(err, "brctl addif")
	}

	// Setup node's network namespace
	err = sudo("ip", "netns", "add", netns).Run()
	if err != nil {
		return rmNode, errors.Wrap(err, "ip netns add")
	}
	err = sudo("ip", "link", "set", vethNode, "netns", netns).Run()
	if err != nil {
		return rmNode, errors.Wrap(err, "ip link set netns")
	}

	// Bring up
	err = sudo("ip", "link", "set", "dev", vethHost, "up").Run()
	if err != nil {
		return rmNode, errors.Wrap(err, "setting "+vethHost+" up")
	}
	err = sudo("ip", "netns", "exec", netns, "ip", "link", "set", "dev", vethNode, "up").Run()
	if err != nil {
		return rmNode, errors.Wrap(err, "setting "+vethNode+" up")
	}

	// Set up IP addresses
	for _, address := range addresses {
		cmd := []string{"ip", "netns", "exec", netns, "ip", "addr", "add", address.Address}
		if address.Gateway != "" {
			cmd = append(cmd, "broadcast", address.Broadcast)
		}
		cmd = append(cmd, "dev", vethNode)
		err = sudo(cmd...).Run()
		err = errors.Wrap(err, "setting "+address.Address+" up")
		if err != nil {
			return rmNode, err
		}
		if address.Gateway != "" {
			err = sudo("ip", "netns", "exec", netns, "ip", "route", "add", "default", "via", address.Gateway).Run()
			err = errors.Wrap(err, "setting route up")
			if err != nil {
				return rmNode, err
			}
		}
	}

	return rmNode, err
}

func assertSendAdvertisement(t *testing.T, nodeName string, keyPair *ssb.KeyPair) bool {
	envVar := "SEND_ADV"
	value := bytes.NewBuffer(nil)
	err := json.NewEncoder(value).Encode(keyPair)
	require.NoError(t, err)

	user := os.Getenv("USER")
	cmd := sudo("ip", "netns", "exec", nodeName, "sudo", "-u", user, "--",
		os.Args[0], "-test.run=TestSendAdvertisement",
	)
	cmd.Env = append(os.Environ(), envVar+"="+string(value.String()))
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err = cmd.Run()
	return assert.NoError(t, err, "did not receive advertisement")
}

func TestSendAdvertisement(t *testing.T) {
	sendAdv := os.Getenv("SEND_ADV")
	if sendAdv == "" {
		return
	}

	var keyPair ssb.KeyPair
	v := bytes.NewBufferString(sendAdv)
	err := json.NewDecoder(v).Decode(&keyPair)
	require.NoError(t, err)

	localAddr, err := net.ResolveUDPAddr("udp", "")
	require.NoError(t, err, "could not resolve listening address")

	adv, err := NewAdvertiser(localAddr, &keyPair)
	require.NoError(t, err, "could not send advertisement")

	err = adv.Start()
	require.NoError(t, err, "could not start advertiser")
	time.Sleep(5 * time.Second)
	adv.Stop()
}

func TestAdvertisementReceived(t *testing.T) {
	recvAdv := os.Getenv("RECV_ADV")
	if recvAdv == "" {
		return
	}

	addr, err := net.ResolveUDPAddr("udp", ":8008")
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(time.Second * 5))

	buf := make([]byte, 128)
	n, _, err := conn.ReadFrom(buf)
	require.NoError(t, err)
	require.Equal(t, recvAdv, string(buf[:n]))
}

func assertReceiveAdvertisement(t *testing.T, nodeName string, expected string) bool {
	envVar := "RECV_ADV"
	user := os.Getenv("USER")
	cmd := sudo("ip", "netns", "exec", nodeName, "sudo", "-u", user, "--",
		os.Args[0], "-test.run=TestAdvertisementReceived",
	)
	cmd.Env = append(os.Environ(), envVar+"="+expected)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	return assert.NoError(t, err, "did not receive advertisement")
}

func TestAdvertisementsOnIPSubnets(t *testing.T) {
	pubKey := makeTestPubKey(t)
	tests := []struct {
		Name                string
		Gateway             NetConfig
		Advertiser          []NetConfig
		AdvertiserPublicKey *ssb.KeyPair
		Peer                NetConfig
		PeerReceived        string
	}{
		{
			Name: "10.0.0.0/24",
			Gateway: NetConfig{
				Address:   "10.0.0.1",
				Broadcast: "10.255.255.255",
			},
			AdvertiserPublicKey: pubKey,
			Advertiser: []NetConfig{
				{
					Address:   "10.0.0.2/8",
					Gateway:   "10.0.0.1",
					Broadcast: "10.255.255.255",
				},
			},
			Peer: NetConfig{
				Address:   "10.0.0.3/8",
				Gateway:   "10.0.0.1",
				Broadcast: "10.255.255.255",
			},
			PeerReceived: "net:10.0.0.2:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		},
		{
			Name: "192.168.0.0/24",
			Gateway: NetConfig{
				Address:   "192.168.0.1",
				Broadcast: "192.168.255.255",
			},
			AdvertiserPublicKey: pubKey,
			Advertiser: []NetConfig{
				{
					Address:   "192.168.0.2/8",
					Gateway:   "192.168.0.1",
					Broadcast: "192.168.255.255",
				},
			},
			Peer: NetConfig{
				Address:   "192.168.0.3/8",
				Gateway:   "192.168.0.1",
				Broadcast: "192.168.255.255",
			},
			PeerReceived: "net:192.168.0.2:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		},
		{
			Name:                "IPv6 Standard",
			Gateway:             NetConfig{},
			AdvertiserPublicKey: pubKey,
			Advertiser: []NetConfig{
				{Address: "fe80::9e4e:36ff:feb3:bbbb/64"},
			},
			Peer: NetConfig{
				Address: "fe80::9e4e:36ff:feb3:cccc/64",
			},
			PeerReceived: "net:[fe80::9e4e:36ff:feb3:bbbb]:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		},
		// Broken: Wrong IP address received
		//		{
		//			Name: "Mixed Advertiser IPv4 + IPv6 -> IPv6",
		//			Gateway: NetConfig{
		//				Address:   "192.168.0.1/8",
		//				Broadcast: "192.168.255.255",
		//			},
		//			AdvertiserPublicKey: pubKey,
		//			Advertiser: []NetConfig{
		//				{
		//					Address:   "192.168.0.2/8",
		//					Gateway:   "192.168.0.1",
		//					Broadcast: "192.168.255.255",
		//				},
		//				{Address: "fe80::9e4e:36ff:feb3:bbbb/64"},
		//			},
		//			Peer: NetConfig{
		//				Address: "fe80::9e4e:36ff:feb3:cccc/64",
		//			},
		//			PeerReceived: "net:[fe80::9e4e:36ff:feb3:bbbb]:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		//		},
		{
			Name: "Mixed Advertiser IPv4 + IPv6 -> IPv4",
			Gateway: NetConfig{
				Address:   "192.168.0.1/8",
				Broadcast: "192.168.255.255",
			},
			AdvertiserPublicKey: pubKey,
			Advertiser: []NetConfig{
				{
					Address:   "192.168.0.2/8",
					Gateway:   "192.168.0.1",
					Broadcast: "192.168.255.255",
				},
				{Address: "fe80::9e4e:36ff:feb3:bbbb/64"},
			},
			Peer: NetConfig{
				Address:   "192.168.0.3/8",
				Gateway:   "192.168.0.1",
				Broadcast: "192.168.255.255",
			},
			PeerReceived: "net:192.168.0.2:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		},
	}

	testNet := "testNet"

	for _, test := range tests {
		func(t *testing.T) {
			t.Log(test.Name)

			// Construct test network
			stopNet, err := createTestNetwork(testNet, test.Gateway)
			require.NoError(t, err)
			defer stopNet()

			rmLocalNode, err := addNode("local", testNet, test.Advertiser...)
			require.NoError(t, err)
			defer rmLocalNode()

			rmPeerNode, err := addNode("peer", testNet, test.Peer)
			require.NoError(t, err)
			defer rmPeerNode()

			go func() {
				// Wait for settings to materialize
				time.Sleep(3 * time.Second)
				assertSendAdvertisement(t, "local", test.AdvertiserPublicKey)
			}()
			assertReceiveAdvertisement(t, "peer", test.PeerReceived)
		}(t)
	}
}