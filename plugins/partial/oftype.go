package partial

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	"go.cryptoscope.co/librarian"
	"go.cryptoscope.co/luigi"
	"go.cryptoscope.co/margaret"
	"go.cryptoscope.co/margaret/multilog/roaring"
	"go.cryptoscope.co/muxrpc"
	refs "go.mindeco.de/ssb-refs"
)

type getMessagesOfTypeHandler struct {
	rxlog margaret.Log

	feeds  *roaring.MultiLog
	bytype *roaring.MultiLog
}

func (h getMessagesOfTypeHandler) HandleSource(ctx context.Context, req *muxrpc.Request, snk luigi.Sink) error {
	if len(req.Args()) < 1 {
		return errors.Errorf("invalid arguments")
	}
	var (
		feed *refs.FeedRef
		tipe string

		keys bool

		err error
	)
	switch v := req.Args()[0].(type) {

	case map[string]interface{}:
		refV, ok := v["id"]
		if !ok {
			return errors.Errorf("invalid argument - missing 'id' in map")
		}

		ref, ok := refV.(string)
		if !ok {
			return errors.Errorf("invalid argument - 'id' field is not a string")
		}

		feed, err = refs.ParseFeedRef(ref)
		if err != nil {
			return fmt.Errorf("invalid argument: %w", err)
		}

		typeV, ok := v["type"]
		if !ok {
			return errors.Errorf("invalid argument - missing 'type' in map")
		}

		tipe = typeV.(string)

		if keysV, has := v["keys"]; has {
			if yes, ok := keysV.(bool); ok {
				keys = yes
			}
		}

	default:
		return errors.Errorf("invalid argument type %T", req.Args()[0])

	}

	workSet, err := h.feeds.LoadInternalBitmap(feed.StoredAddr())
	if err != nil {
		// TODO actual assert not found error.
		// errors.Errorf("failed to load feed %s bitmap: %s", feed.ShortRef(), err.Error())
		snk.Close()
		return nil

	}

	tipeSeqs, err := h.bytype.LoadInternalBitmap(librarian.Addr(tipe))
	if err != nil {
		// return errors.Errorf("failed to load msg type %s bitmap: %s", tipe, err.Error())
		snk.Close()
		return nil

	}

	// which sequences are in both?
	workSet.And(tipeSeqs)

	it := workSet.Iterator()
	for it.HasNext() {

		v := it.Next()
		msgv, err := h.rxlog.Get(margaret.BaseSeq(v))
		if err != nil {
			break
		}

		msg, ok := msgv.(refs.Message)
		if !ok {
			return errors.Errorf("invalid msg type %T", msgv)
		}
		if keys {
			var kv refs.KeyValueRaw
			kv.Key_ = msg.Key()
			kv.Value = *msg.ValueContent()
			b, err := json.Marshal(kv)
			if err != nil {
				return errors.Errorf("failed to encode json: %w", err)
			}
			err = snk.Pour(ctx, json.RawMessage(b))
			if err != nil {
				return fmt.Errorf("failed to send json data: %w", err)
			}
		} else {
			err = snk.Pour(ctx, msg.ValueContentJSON())
			if err != nil {
				return fmt.Errorf("failed to send json data: %w", err)
			}
		}
	}
	snk.Close()
	return nil
}
