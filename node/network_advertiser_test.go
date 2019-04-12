package node

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.cryptoscope.co/ssb"
)

func makeTestPubKey(t *testing.T) *ssb.KeyPair {
	var kp ssb.KeyPair
	fr, err := ssb.ParseFeedRef("@LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=.ed25519")
	if err != nil {
		t.Fatal(err)
	}
	copy(kp.Pair.Public[:], fr.ID)
	kp.Id = fr
	return &kp
}

func TestNewAdvertisement(t *testing.T) {
	type tcase struct {
		local       *net.UDPAddr
		keyPair     *ssb.KeyPair
		Expected    string
		ExpectError bool
	}

	tests := []tcase{
		{
			local: &net.UDPAddr{
				IP:   net.IPv4(1, 2, 3, 4),
				Port: 8008,
			},
			keyPair:     makeTestPubKey(t),
			ExpectError: false,
			Expected:    "net:1.2.3.4:8008~shs:LtQ3tOuLoeQFi5s/ic7U6wDBxWS3t2yxauc4/AwqfWc=",
		},
	}

	for _, test := range tests {
		res, err := newAdvertisement(test.local, test.keyPair)
		if test.ExpectError {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, test.Expected, res)
	}
}

func TestBendTCPAddr(t *testing.T) {
	r := require.New(t)

	pk := makeTestPubKey(t)

	senderAddr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8008")
	r.NoError(err)

	adv, err := NewAdvertiser(senderAddr, pk)
	r.NoError(err)

	r.NoError(adv.Start(), "couldn't start 1")

	time.Sleep(time.Second * 2)
	adv.Stop()
}

func TestUDPSend(t *testing.T) {
	r := require.New(t)

	pk := makeTestPubKey(t)

	senderAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:8008")
	r.NoError(err)

	adv, err := NewAdvertiser(senderAddr, pk)
	r.NoError(err)

	r.NoError(adv.Start(), "couldn't start 1")

	ch, done := adv.Notify()

	addr := <-ch
	r.Equal("127.0.0.1:8008", addr.String())
	done()

	adv.Stop()
}

/*  TODO: test two on different networks

senderAddr, err := net.ResolveUDPAddr("udp", "192.168.42.21:8008")
r.NoError(err)

adv, err := NewAdvertiser(senderAddr, pk)
r.NoError(err)

// senderAddr, err = net.ResolveUDPAddr("udp", "127.0.0.2:8008")
// r.NoError(err)
// adv2, err := NewAdvertiser(senderAddr, pk)
// r.NoError(err)

r.NoError(adv.Start(), "couldn't start 1")
// r.NoError(adv2.Start(), "couldn't start 2")
*/