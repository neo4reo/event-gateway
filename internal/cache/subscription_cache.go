package cache

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/serverless/event-gateway/event"
	"github.com/serverless/event-gateway/functions"
	"github.com/serverless/event-gateway/internal/pathtree"
	"github.com/serverless/event-gateway/subscriptions"
	"go.uber.org/zap"
)

type subscriptionCache struct {
	sync.RWMutex
	eventToFunctions map[string]map[event.Type][]functions.FunctionID
	// endpoints maps HTTP method to internal/pathtree. Tree struct which is used for resolving HTTP requests paths.
	endpoints map[string]*pathtree.Node
	// invokable stores functions that have invoke subscription
	invokable map[string]map[functions.FunctionID]struct{}
	log       *zap.Logger
}

func newSubscriptionCache(log *zap.Logger) *subscriptionCache {
	return &subscriptionCache{
		eventToFunctions: map[string]map[event.Type][]functions.FunctionID{},
		endpoints:        map[string]*pathtree.Node{},
		invokable:        map[string]map[functions.FunctionID]struct{}{},
		log:              log,
	}
}

func (c *subscriptionCache) Modified(k string, v []byte) {
	c.log.Debug("Subscription local cache received value update.", zap.String("key", k), zap.String("value", string(v)))

	s := subscriptions.Subscription{}
	err := json.NewDecoder(bytes.NewReader(v)).Decode(&s)
	if err != nil {
		c.log.Error("Could not deserialize Subscription state.", zap.Error(err), zap.String("key", k), zap.String("value", string(v)))
		return
	}
	c.Lock()
	defer c.Unlock()

	if s.Event == event.TypeHTTP {
		root := c.endpoints[s.Method]
		if root == nil {
			root = pathtree.NewNode()
			c.endpoints[s.Method] = root
		}
		err := root.AddRoute(s.Path, s.FunctionID, s.CORS)
		if err != nil {
			c.log.Error("Could not add path to the tree.", zap.Error(err), zap.String("path", s.Path), zap.String("method", s.Method))
		}
	} else if s.Event == event.TypeInvoke {
		fnSet, exists := c.invokable[s.Path]
		if exists {
			fnSet[s.FunctionID] = struct{}{}
		} else {
			fnSet := map[functions.FunctionID]struct{}{}
			fnSet[s.FunctionID] = struct{}{}
			c.invokable[s.Path] = fnSet
		}
	} else {
		c.createPath(s.Path)
		ids, exists := c.eventToFunctions[s.Path][s.Event]
		if exists {
			ids = append(ids, s.FunctionID)
		} else {
			ids = []functions.FunctionID{s.FunctionID}
		}
		c.eventToFunctions[s.Path][s.Event] = ids

	}
}

func (c *subscriptionCache) Deleted(k string, v []byte) {
	c.Lock()
	defer c.Unlock()

	oldSub := subscriptions.Subscription{}
	err := json.NewDecoder(bytes.NewReader(v)).Decode(&oldSub)
	if err != nil {
		c.log.Error("Could not deserialize Subscription state during deletion.", zap.Error(err), zap.String("key", k))
		return
	}

	if oldSub.Event == event.TypeHTTP {
		c.deleteEndpoint(oldSub)
	} else if oldSub.Event == event.TypeInvoke {
		c.deleteInvokable(oldSub)
	} else {
		c.deleteSubscription(oldSub)
	}
}

func (c *subscriptionCache) createPath(path string) {
	_, exists := c.eventToFunctions[path]
	if !exists {
		c.eventToFunctions[path] = map[event.Type][]functions.FunctionID{}
	}
}

func (c *subscriptionCache) deleteEndpoint(sub subscriptions.Subscription) {
	root := c.endpoints[sub.Method]
	if root == nil {
		return
	}
	err := root.DeleteRoute(sub.Path)
	if err != nil {
		c.log.Error("Could not delete path from the tree.", zap.Error(err), zap.String("path", sub.Path), zap.String("method", sub.Method))
	}
}

func (c *subscriptionCache) deleteInvokable(sub subscriptions.Subscription) {
	fnSet, exists := c.invokable[sub.Path]
	if exists {
		delete(fnSet, sub.FunctionID)

		if len(fnSet) == 0 {
			delete(c.invokable, sub.Path)
		}
	}
}

func (c *subscriptionCache) deleteSubscription(sub subscriptions.Subscription) {
	ids, exists := c.eventToFunctions[sub.Path][sub.Event]
	if exists {
		for i, id := range ids {
			if id == sub.FunctionID {
				ids = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		c.eventToFunctions[sub.Path][sub.Event] = ids

		if len(ids) == 0 {
			delete(c.eventToFunctions[sub.Path], sub.Event)
		}
	}
}
