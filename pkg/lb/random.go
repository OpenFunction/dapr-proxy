package lb

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

var seededRand = rand.New(rand.NewSource(time.Now().UnixNano()))
var seedMutex sync.Mutex

func inRange(min, max int64) int64 {
	seedMutex.Lock()
	defer seedMutex.Unlock()
	return seededRand.Int63n(max-min) + min
}

// New make a new load-balancer instance with Round-Robin
func New(opts ...Opt) Balancer {
	return (&random{}).init(opts...)
}

type random struct {
	Endpoints []Endpoint
	count     int64
	rw        sync.RWMutex
}

func (s *random) init(opts ...Opt) *random {
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *random) String() string { return "random" }

func (s *random) Next(factor Factor) (next Endpoint, c Constrainable) {
	next = s.next()
	if fc, ok := factor.(FactorComparable); ok {
		next, c, _ = fc.ConstrainedBy(next)
	} else if nested, ok := next.(balancer); ok {
		next, c = nested.Next(factor)
	}
	return
}

func (s *random) next() (next Endpoint) {
	s.rw.RLock()
	defer s.rw.RUnlock()

	if l := len(s.Endpoints); l > 0 {
		ni := atomic.AddInt64(&s.count, inRange(0, int64(l))) % int64(l)
		next = s.Endpoints[ni]
	}
	return
}

func (s *random) Count() int {
	s.rw.RLock()
	defer s.rw.RUnlock()
	return len(s.Endpoints)
}

func (s *random) All() []Endpoint {
	s.rw.RLock()
	defer s.rw.RUnlock()
	return s.Endpoints
}

func (s *random) Add(Endpoints ...Endpoint) {
	for _, p := range Endpoints {
		s.AddOne(p)
	}
}

func (s *random) AddOne(Endpoint Endpoint) {
	if s.find(Endpoint) {
		return
	}

	s.rw.Lock()
	defer s.rw.Unlock()
	s.Endpoints = append(s.Endpoints, Endpoint)
}

func (s *random) find(Endpoint Endpoint) (found bool) {
	s.rw.RLock()
	defer s.rw.RUnlock()
	for _, p := range s.Endpoints {
		if DeepEqual(p, Endpoint) {
			return true
		}
	}
	return
}

func (s *random) Remove(Endpoint Endpoint) {
	s.rw.Lock()
	defer s.rw.Unlock()
	for i, p := range s.Endpoints {
		if DeepEqual(p, Endpoint) {
			s.Endpoints = append(s.Endpoints[0:i], s.Endpoints[i+1:]...)
			return
		}
	}
}

func (s *random) Clear() {
	s.rw.Lock()
	defer s.rw.Unlock()
	s.Endpoints = nil
}
