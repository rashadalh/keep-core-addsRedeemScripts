package local

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/keep-network/keep-core/pkg/net"
	"github.com/keep-network/keep-core/pkg/net/key"
)

func TestRegisterAndFireHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, localChannel, err := initTestChannel("channel name")
	if err != nil {
		t.Fatal(err)
	}

	handlerFiredChan := make(chan struct{})
	handler := net.HandleMessageFunc{
		Type: "rambo",
		Handler: func(msg net.Message) error {
			handlerFiredChan <- struct{}{}
			return nil
		},
	}

	localChannel.Recv(handler)

	localChannel.Send(&mockNetMessage{})

	select {
	case <-handlerFiredChan:
		return
	case <-ctx.Done():
		t.Errorf("Expected handler not called")
	}
}

func TestUnregisterHandler(t *testing.T) {
	tests := map[string]struct {
		handlersRegistered   []string
		handlersUnregistered []string
		handlersFired        []string
	}{
		"unregister the first registered handler": {
			handlersRegistered:   []string{"a", "b", "c"},
			handlersUnregistered: []string{"a"},
			handlersFired:        []string{"b", "c"},
		},
		"unregister the last registered handler": {
			handlersRegistered:   []string{"a", "b", "c"},
			handlersUnregistered: []string{"c"},
			handlersFired:        []string{"a", "b"},
		},
		"unregister handler registered in the middle": {
			handlersRegistered:   []string{"a", "b", "c"},
			handlersUnregistered: []string{"b"},
			handlersFired:        []string{"a", "c"},
		},
		"unregister various handlers": {
			handlersRegistered:   []string{"a", "b", "c", "d", "e", "f", "g"},
			handlersUnregistered: []string{"a", "c", "f", "g"},
			handlersFired:        []string{"b", "d", "e"},
		},
		"unregister all handlers": {
			handlersRegistered:   []string{"a", "b", "c"},
			handlersUnregistered: []string{"a", "b", "c"},
			handlersFired:        []string{},
		},
		"unregister two first registered handlers with the same type": {
			handlersRegistered:   []string{"a", "a", "b", "c", "d"},
			handlersUnregistered: []string{"a"},
			handlersFired:        []string{"b", "c", "d"},
		},
		"unregister two last registered handlers with the same type": {
			handlersRegistered:   []string{"a", "b", "c", "d", "d"},
			handlersUnregistered: []string{"d"},
			handlersFired:        []string{"a", "b", "c"},
		},
		"unregister various handlers with the same type": {
			handlersRegistered:   []string{"a", "f", "b", "e", "c", "f", "e"},
			handlersUnregistered: []string{"e", "f"},
			handlersFired:        []string{"a", "b", "c"},
		},
		"unregister handler not previously registered": {
			handlersRegistered:   []string{"a", "b", "c"},
			handlersUnregistered: []string{"z"},
			handlersFired:        []string{"a", "b", "c"},
		},
	}

	for testName, test := range tests {
		test := test
		t.Run(testName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			_, localChannel, err := initTestChannel("channel name")
			if err != nil {
				t.Fatal(err)
			}

			handlersFiredMutex := &sync.Mutex{}
			handlersFired := []string{}

			// Register all handlers. If the handler is called, append its
			// type to `handlersFired` slice.
			for _, handlerType := range test.handlersRegistered {
				handlerType := handlerType
				handler := net.HandleMessageFunc{
					Type: handlerType,
					Handler: func(msg net.Message) error {
						handlersFiredMutex.Lock()
						handlersFired = append(handlersFired, handlerType)
						handlersFiredMutex.Unlock()
						return nil
					},
				}

				localChannel.Recv(handler)
			}

			// Unregister specified handlers.
			for _, handlerType := range test.handlersUnregistered {
				localChannel.UnregisterRecv(handlerType)
			}

			// Send a message, all handlers should be called.
			localChannel.Send(&mockNetMessage{})

			// Handlers are fired asynchronously; wait for them.
			<-ctx.Done()

			sort.Strings(handlersFired)
			if !reflect.DeepEqual(test.handlersFired, handlersFired) {
				t.Errorf(
					"Unexpected handlers fired\nExpected: %v\nActual:   %v\n",
					test.handlersFired,
					handlersFired,
				)
			}
		})
	}
}

func TestSendAndDeliver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	msgToSend := &mockNetMessage{}

	channelName := "channel name"

	staticKey1, localChannel1, err := initTestChannel(channelName)
	if err != nil {
		t.Fatal(err)
	}
	_, localChannel2, err := initTestChannel(channelName)
	if err != nil {
		t.Fatal(err)
	}
	_, localChannel3, err := initTestChannel(channelName)
	if err != nil {
		t.Fatal(err)
	}

	// Register handlers.
	inMsgChan := make(chan net.Message, 3)

	msgHandler := net.HandleMessageFunc{
		Type: msgToSend.Type(),
		Handler: func(msg net.Message) error {
			inMsgChan <- msg
			return nil
		},
	}

	if err := localChannel1.Recv(msgHandler); err != nil {
		t.Fatalf("failed to register receive handler: [%v]", err)
	}
	if err := localChannel2.Recv(msgHandler); err != nil {
		t.Fatalf("failed to register receive handler: [%v]", err)
	}
	if err := localChannel3.Recv(msgHandler); err != nil {
		t.Fatalf("failed to register receive handler: [%v]", err)
	}

	// Broadcast message by the first peer.
	if err := localChannel1.Send(msgToSend); err != nil {
		t.Fatalf("failed to send message: [%v]", err)
	}

	deliveredMessages := []net.Message{}
	go func() {
		for {
			select {
			case msg := <-inMsgChan:
				deliveredMessages = append(deliveredMessages, msg)
			}
		}
	}()

	<-ctx.Done()

	if len(deliveredMessages) != 3 {
		t.Errorf("unexpected number of delivered messages: [%d]", len(deliveredMessages))
	}

	for _, msg := range deliveredMessages {
		if !reflect.DeepEqual(msgToSend, msg.Payload()) {
			t.Errorf(
				"invalid payload\nexpected: [%+v]\nactual:   [%+v]\n",
				msgToSend,
				msg.Payload(),
			)
		}
		if "local" != msg.Type() {
			t.Errorf(
				"invalid type\nexpected: [%+v]\nactual:   [%+v]\n",
				"local",
				msg.Type(),
			)
		}
		if !bytes.Equal(key.Marshal(staticKey1), msg.SenderPublicKey()) {
			t.Errorf(
				"invalid sender public key\nexpected: [%+v]\nactual:   [%+v]\n",
				key.Marshal(staticKey1),
				msg.SenderPublicKey(),
			)
		}
	}
}

func initTestChannel(channelName string) (*key.NetworkPublic, net.BroadcastChannel, error) {
	_, staticKey, err := key.GenerateStaticNetworkKey()
	if err != nil {
		return nil, nil, err
	}

	provider := ConnectWithKey(staticKey)
	localChannel, err := provider.ChannelFor(channelName)
	if err != nil {
		return nil, nil, err
	}
	localChannel.RegisterUnmarshaler(func() net.TaggedUnmarshaler {
		return &mockNetMessage{}
	})

	return staticKey, localChannel, nil
}

type mockNetMessage struct{}

func (mm *mockNetMessage) Type() string {
	return "mock_message"
}

func (mm *mockNetMessage) Marshal() ([]byte, error) {
	return []byte("some mocked bytes"), nil
}

func (mm *mockNetMessage) Unmarshal(bytes []byte) error {
	return nil
}
