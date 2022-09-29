package lb

import "reflect"

// Endpoint is a backend object, such as a host+port, a
// http/https url, or a constraint expression, and so on.
type Endpoint interface {
	// String will return the main identity string of a endpoint.
	String() string
}

// Factor is a factor parameter for balancer.Next.
type Factor interface {
	Factor() string
}

// balancer represents a generic load balancer.
type balancer interface {
	// Next will return the next backend.
	Next(factor Factor) (next Endpoint, c Constrainable)
}

// Balancer represents a generic load balancer.
// For the real world, Balancer is a useful interface rather
// than balancer.
type Balancer interface {
	balancer
	Count() int
	All() []Endpoint
	Add(peers ...Endpoint)
	Remove(peer Endpoint)
	Clear()
}

// Opt is a type prototype for New Balancer
type Opt func(balancer Balancer)

// FactorComparable is a composite interface which assembly Factor and constraint comparing.
type FactorComparable interface {
	Factor
	ConstrainedBy(constraints interface{}) (peer Endpoint, c Constrainable, satisfied bool)
}

// FactorString is a string type, it implements Factor interface.
type FactorString string

// Factor function impl Factor interface
func (s FactorString) Factor() string { return string(s) }

// DummyFactor will be used in someone like you does not known
// what on earth should be passed into balancer.Next(factor).
const DummyFactor FactorString = "robin"

// Constrainable is an object who can be applied onto a factor ( balancer.Next(factor) )
type Constrainable interface {
	CanConstrain(o interface{}) (yes bool)
	Check(o interface{}) (satisfied bool)
	Endpoint
}

// DeepEqualAware could be concreted by an Endpoint
type DeepEqualAware interface {
	DeepEqual(b Endpoint) bool
}

// DeepEqual will be used in Balancer, and a Endpoint can bypass
// reflect.DeepEqual by implementing DeepEqualAware interface.
func DeepEqual(a, b Endpoint) (yes bool) {
	if a == b {
		return true
	}

	if e, ok := a.(DeepEqualAware); ok {
		return e.DeepEqual(b)
	}

	return reflect.DeepEqual(a, b)
}
