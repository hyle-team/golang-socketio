package gosocketio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vladivolo/golang-socketio/protocol"
	"os"
	"reflect"
	"strings"
	"sync"
)

const (
	OnConnection    = "connection"
	OnDisconnection = "disconnection"
	OnError         = "error"
)

/**
System handler function for internal event processing
*/
type systemHandler func(c *Channel)

/**
Contains maps of message processing functions
*/
type methods struct {
	messageHandlers     map[string]*caller
	messageHandlersLock sync.RWMutex

	onConnection    systemHandler
	onDisconnection systemHandler
}

/**
create messageHandlers map
*/
func (m *methods) initMethods() {
	m.messageHandlers = make(map[string]*caller)
}

/**
Add message processing function, and bind it to given method
*/
func (m *methods) On(method string, f interface{}, channels ...string) error {
	c, err := newCaller(f)
	if err != nil {
		return err
	}

	m.messageHandlersLock.Lock()
	defer m.messageHandlersLock.Unlock()
	m.messageHandlers[method] = c
	if len(channels) > 0 {
		m.messageHandlers[method+strings.ToLower(channels[0])] = c
	}

	return nil
}

/**
Find message processing function associated with given method
*/
func (m *methods) findMethod(method string, channels ...string) (*caller, bool) {
	m.messageHandlersLock.RLock()
	defer m.messageHandlersLock.RUnlock()

	var (
		f  *caller
		ok bool
	)
	if len(channels) > 0 {
		f, ok = m.messageHandlers[method+strings.ToLower(channels[0])]
	}
	if !ok {
		f, ok = m.messageHandlers[method]
	}
	return f, ok
}

func (m *methods) callLoopEvent(c *Channel, event string) {
	if m.onConnection != nil && event == OnConnection {
		m.onConnection(c)
	}
	if m.onDisconnection != nil && event == OnDisconnection {
		m.onDisconnection(c)
	}

	f, ok := m.findMethod(event)
	if !ok {
		return
	}

	f.callFunc(c, &struct{}{})
}

/**
Check incoming message
On ack_resp - look for waiter
On ack_req - look for processing function and send ack_resp
On emit - look for processing function
*/
func (m *methods) processIncomingMessage(c *Channel, msg *protocol.Message) {
	switch msg.Type {
	case protocol.MessageTypeEmit:
		f, ok := m.findMethod(msg.Method)
		if !ok {
			return
		}

		if !f.ArgsPresent {
			f.callFunc(c, &struct{}{})
			return
		}

		args := []byte(msg.Args)

		// Patch for stex.com custom responce (trim prefix "\"channel_name\"," )
		ch := []byte{}
		idx := bytes.IndexAny(args, "{[")
		if idx > 0 {
			ch = args[1:idx]
			idx_last := bytes.IndexAny(ch, "\"")
			if idx_last > 0 {
				ch = ch[:idx_last]
				ff, ok := m.findMethod(msg.Method, string(ch))
				if ok {
					f = ff
				}

			}
			args = args[idx:]
		}

		// end patch

		data := f.getArgs()
		err := json.Unmarshal(args, &data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "json.Unmarshal() %s\n", err)
			return
		}

		fmt.Printf("MSG: [%s] <%v>\n", string(ch), data)

		f.callFunc(c, data)

		return

	case protocol.MessageTypeAckRequest:
		f, ok := m.findMethod(msg.Method)
		if !ok || !f.Out {
			return
		}

		var result []reflect.Value
		if f.ArgsPresent {
			//data type should be defined for unmarshall
			data := f.getArgs()
			err := json.Unmarshal([]byte(msg.Args), &data)
			if err != nil {
				return
			}

			result = f.callFunc(c, data)
		} else {
			result = f.callFunc(c, &struct{}{})
		}

		ack := &protocol.Message{
			Type:  protocol.MessageTypeAckResponse,
			AckId: msg.AckId,
		}
		send(ack, c, result[0].Interface())

	case protocol.MessageTypeAckResponse:
		waiter, err := c.ack.getWaiter(msg.AckId)
		if err == nil {
			waiter <- msg.Args
		}
	}
}
