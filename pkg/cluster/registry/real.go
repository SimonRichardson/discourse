package registry

import (
	"sync"

	"github.com/SimonRichardson/alchemy/pkg/cluster/hashring"
	"github.com/SimonRichardson/alchemy/pkg/cluster/members"
)

type real struct {
	mtx               sync.RWMutex
	hashRings         map[string]*hashring.HashRing
	keys              map[string]map[string]Key
	hashFn            func([]byte) uint32
	replicationFactor int
}

func New(hashFn func([]byte) uint32, replicationFactor int) Registry {
	return &real{
		hashRings:         make(map[string]*hashring.HashRing),
		keys:              make(map[string]map[string]Key),
		hashFn:            hashFn,
		replicationFactor: replicationFactor,
	}
}

func (r *real) Add(key Key) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	keyType := key.Type()
	if _, ok := r.hashRings[keyType]; !ok {
		r.hashRings[keyType] = hashring.New(r.hashFn, r.replicationFactor)
	}

	var (
		addr = key.Address()
		res  = r.hashRings[keyType].Add(addr)
	)
	if _, ok := r.keys[addr]; !ok {
		r.keys[addr] = make(map[string]Key)
	}
	r.keys[addr][key.Name()] = key

	return res
}

func (r *real) Remove(key Key) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	var (
		keyType = key.Type()
		addr    = key.Address()
	)
	if _, ok := r.hashRings[keyType]; ok {
		r.hashRings[keyType].Remove(addr)
	}
	if keys, ok := r.keys[addr]; ok {
		delete(keys, key.Name())
	}
	return true
}

func (r *real) Update(key Key) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	var (
		keyType = key.Type()
		addr    = key.Address()
	)
	if _, ok := r.hashRings[keyType]; !ok || (ok && !r.hashRings[keyType].Contains(addr)) {
		return false
	}

	if _, ok := r.keys[addr]; !ok {
		return false
	}

	name := key.Name()
	if _, ok := r.keys[addr][name]; !ok {
		return false
	}
	r.keys[addr][name] = key

	return true
}

func (r *real) Info(s string) (Info, bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	hashRing, ok := r.hashRings[s]
	if !ok {
		return Info{}, false
	}

	hashes := make(map[string]string)
	if err := hashRing.Walk(func(hash, addr string) error {
		hashes[hash] = addr
		return nil
	}); err != nil {
		return Info{}, false
	}

	keys := make(map[string][]Key)
	for _, v := range hashes {
		if k := r.getKeysByAddress(v); len(k) > 0 {
			keys[v] = append(keys[v], k...)
		}
	}

	return Info{
		Hashes: hashes,
		Keys:   keys,
	}, true
}

func (r *real) getKeysByAddress(addr string) (res []Key) {
	if keys, ok := r.keys[addr]; ok {
		for _, v := range keys {
			res = append(res, v)
		}
	}
	return
}

type key struct {
	member members.Member
}

func NewMemberKey(member members.Member) Key {
	return &key{member}
}

func (k *key) Name() string {
	return k.member.Name()
}

func (k *key) Type() string {
	return k.member.PeerType().String()
}

func (k *key) Address() string {
	return k.member.Address()
}

func (k *key) Tags() map[string]string {
	return k.member.Tags()
}
