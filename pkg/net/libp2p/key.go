package libp2p

import (
	"crypto/elliptic"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/keep-network/keep-core/pkg/operator"
	libp2pcrypto "github.com/libp2p/go-libp2p-core/crypto"
)

// DefaultCurve is the default elliptic curve implementation used in the
// net/libp2p package. LibP2P network uses the secp256k1 curve and the specific
// implementation is provided by the btcec package.
var DefaultCurve elliptic.Curve = btcec.S256()

// operatorPrivateKeyToNetworkKeyPair converts an operator private key to
// the libp2p network key pair that uses the libp2p-specific curve
// implementation.
func operatorPrivateKeyToNetworkKeyPair(operatorPrivateKey *operator.PrivateKey) (
	*libp2pcrypto.Secp256k1PrivateKey,
	*libp2pcrypto.Secp256k1PublicKey,
	error,
) {
	// Make sure that libp2p package receives only secp256k1 operator keys.
	if operatorPrivateKey.Curve != operator.Secp256k1 {
		return nil, nil, fmt.Errorf("libp2p supports only secp256k1 operator keys")
	}

	btcecPrivateKey, btcecPublickKey := btcec.PrivKeyFromBytes(
		operatorPrivateKey.D.Bytes(),
	)

	networkPrivateKey := libp2pcrypto.Secp256k1PrivateKey(*btcecPrivateKey)
	networkPublicKey := libp2pcrypto.Secp256k1PublicKey(*btcecPublickKey)

	return &networkPrivateKey, &networkPublicKey, nil
}

// operatorPublicKeyToNetworkPublicKey converts an operator public key to
// the libp2p network public key that uses the libp2p-specific curve
// implementation.
func operatorPublicKeyToNetworkPublicKey(
	operatorPublicKey *operator.PublicKey,
) (*libp2pcrypto.Secp256k1PublicKey, error) {
	// Make sure that libp2p package receives only secp256k1 operator keys.
	if operatorPublicKey.Curve != operator.Secp256k1 {
		return nil, fmt.Errorf("libp2p supports only secp256k1 operator keys")
	}

	operatorPublicKeyBytes := operator.MarshalCompressed(operatorPublicKey)

	btcecPublicKey, err := btcec.ParsePubKey(operatorPublicKeyBytes)
	if err != nil {
		return nil, err
	}

	networkPublicKey := libp2pcrypto.Secp256k1PublicKey(*btcecPublicKey)

	return &networkPublicKey, nil
}

// networkPublicKeyToOperatorPublicKey converts a libp2p network public key to
// the operator public key that is agnostic regarding the curve implementation.
func networkPublicKeyToOperatorPublicKey(
	networkPublicKey libp2pcrypto.PubKey,
) (*operator.PublicKey, error) {
	switch publicKey := networkPublicKey.(type) {
	case *libp2pcrypto.Secp256k1PublicKey:
		btcecPublicKey := (*btcec.PublicKey)(publicKey)
		return &operator.PublicKey{
			Curve: operator.Secp256k1,
			X:     btcecPublicKey.X(),
			Y:     btcecPublicKey.Y(),
		}, nil
	}
	return nil, fmt.Errorf("unrecognized libp2p public key type")
}
