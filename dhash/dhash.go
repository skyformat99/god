package dhash

import (
	"../common"
	"../discord"
	"../murmur"
	"../radix"
	"../timenet"
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type SyncListener func(dhash *Node, fetched, distributed int)
type CleanListener func(dhash *Node, cleaned, redistributed int)
type MigrateListener func(dhash *Node, source, destination []byte)

const (
	syncInterval      = time.Second
	migrateHysteresis = 1.5
	migrateWaitFactor = 2
)

const (
	created = iota
	started
	stopped
)

type Node struct {
	state            int32
	lastSync         int64
	lastMigrate      int64
	lastReroute      int64
	lock             *sync.RWMutex
	syncListeners    []SyncListener
	cleanListeners   []CleanListener
	migrateListeners []MigrateListener
	node             *discord.Node
	timer            *timenet.Timer
	tree             *radix.Tree
}

func NewNode(addr string) (result *Node) {
	result = &Node{
		node:  discord.NewNode(addr),
		lock:  new(sync.RWMutex),
		state: created,
	}
	result.AddChangeListener(func(r *common.Ring) {
		atomic.StoreInt64(&result.lastReroute, time.Now().UnixNano())
	})
	result.timer = timenet.NewTimer((*dhashPeerProducer)(result))
	result.tree = radix.NewTreeTimer(result.timer).Log(addr).Restore()
	result.node.Export("Timenet", (*timerServer)(result.timer))
	result.node.Export("DHash", (*dhashServer)(result))
	result.node.Export("HashTree", (*hashTreeServer)(result))
	return
}
func (self *Node) AddCleanListener(l CleanListener) {
	self.lock.Lock()
	defer self.lock.Unlock()
	self.cleanListeners = append(self.cleanListeners, l)
}
func (self *Node) AddMigrateListener(l MigrateListener) {
	self.lock.Lock()
	defer self.lock.Unlock()
	self.migrateListeners = append(self.migrateListeners, l)
}
func (self *Node) AddSyncListener(l SyncListener) {
	self.lock.Lock()
	defer self.lock.Unlock()
	self.syncListeners = append(self.syncListeners, l)
}
func (self *Node) hasState(s int32) bool {
	return atomic.LoadInt32(&self.state) == s
}
func (self *Node) changeState(old, neu int32) bool {
	return atomic.CompareAndSwapInt32(&self.state, old, neu)
}
func (self *Node) GetAddr() string {
	return self.node.GetAddr()
}
func (self *Node) AddChangeListener(f common.RingChangeListener) {
	self.node.AddChangeListener(f)
}
func (self *Node) Stop() {
	if self.changeState(started, stopped) {
		self.node.Stop()
		self.timer.Stop()
	}
}
func (self *Node) Start() (err error) {
	if !self.changeState(created, started) {
		return fmt.Errorf("%v can only be started when in state 'created'", self)
	}
	if err = self.node.Start(); err != nil {
		return
	}
	self.timer.Start()
	go self.syncPeriodically()
	go self.cleanPeriodically()
	go self.migratePeriodically()
	return
}
func (self *Node) sync() {
	fetched := 0
	distributed := 0
	nextSuccessor := self.node.GetSuccessor()
	for i := 0; i < self.node.Redundancy()-1; i++ {
		myPos := self.node.GetPosition()
		distributed += radix.NewSync(self.tree, (remoteHashTree)(nextSuccessor)).From(self.node.GetPredecessor().Pos).To(myPos).Run().PutCount()
		fetched += radix.NewSync((remoteHashTree)(nextSuccessor), self.tree).From(self.node.GetPredecessor().Pos).To(myPos).Run().PutCount()
		nextSuccessor = self.node.GetSuccessorForRemote(nextSuccessor)
	}
	if fetched != 0 || distributed != 0 {
		self.lock.RLock()
		defer self.lock.RUnlock()
		for _, l := range self.syncListeners {
			l(self, fetched, distributed)
		}
	}
}
func (self *Node) syncPeriodically() {
	for self.hasState(started) {
		self.sync()
		time.Sleep(syncInterval)
	}
}
func (self *Node) cleanPeriodically() {
	for self.hasState(started) {
		self.clean()
		time.Sleep(syncInterval)
	}
}
func (self *Node) changePosition(newPos []byte) {
	for len(newPos) < murmur.Size {
		newPos = append(newPos, 0)
	}
	oldPos := self.node.GetPosition()
	if bytes.Compare(newPos, oldPos) != 0 {
		self.node.SetPosition(newPos)
		atomic.StoreInt64(&self.lastMigrate, time.Now().UnixNano())
		self.lock.RLock()
		defer self.lock.RUnlock()
		for _, l := range self.migrateListeners {
			l(self, oldPos, newPos)
		}
	}
}
func (self *Node) isLeader() bool {
	return bytes.Compare(self.node.GetPredecessor().Pos, self.node.GetPosition()) > 0
}
func (self *Node) migratePeriodically() {
	for self.hasState(started) {
		self.migrate()
		time.Sleep(syncInterval)
	}
}
func (self *Node) migrate() {
	lastAllowedChange := time.Now().Add(-1 * migrateWaitFactor * syncInterval).UnixNano()
	if lastAllowedChange > common.Max64(atomic.LoadInt64(&self.lastSync), atomic.LoadInt64(&self.lastReroute), atomic.LoadInt64(&self.lastMigrate)) {
		var succSize int
		succ := self.node.GetSuccessor()
		if err := succ.Call("DHash.Owned", 0, &succSize); err != nil {
			self.node.RemoveNode(succ)
		} else {
			mySize := self.Owned()
			if mySize > 10 && float64(mySize) > float64(succSize)*migrateHysteresis {
				wantedDelta := (mySize - succSize) / 2
				var existed bool
				var wantedPos []byte
				pred := self.node.GetPredecessor()
				if bytes.Compare(pred.Pos, self.node.GetPosition()) < 1 {
					if wantedPos, existed = self.tree.NextMarkerIndex(self.tree.RealSizeBetween(nil, self.node.GetPosition(), true, false) - wantedDelta); !existed {
						return
					}
				} else {
					ownedAfterNil := self.tree.RealSizeBetween(nil, succ.Pos, true, false)
					if ownedAfterNil > wantedDelta {
						if wantedPos, existed = self.tree.NextMarkerIndex(ownedAfterNil - wantedDelta); !existed {
							return
						}
					} else {
						if wantedPos, existed = self.tree.NextMarkerIndex(self.tree.RealSize() + ownedAfterNil - wantedDelta); !existed {
							return
						}
					}
				}
				if common.BetweenIE(wantedPos, self.node.GetPredecessor().Pos, self.node.GetPosition()) {
					self.changePosition(wantedPos)
				}
			}
		}
	}
}
func (self *Node) circularNext(key []byte) (nextKey []byte, existed bool) {
	if nextKey, existed = self.tree.NextMarker(key); existed {
		return
	}
	nextKey = make([]byte, murmur.Size)
	if _, _, existed = self.tree.Get(nextKey); existed {
		return
	}
	nextKey, existed = self.tree.NextMarker(nextKey)
	return
}
func (self *Node) owners(key []byte) (owners common.Remotes, isOwner bool) {
	owners = append(owners, self.node.GetSuccessorFor(key))
	if owners[0].Addr == self.node.GetAddr() {
		isOwner = true
	}
	for i := 1; i < self.node.Redundancy(); i++ {
		owners = append(owners, self.node.GetSuccessorForRemote(owners[i-1]))
		if owners[i].Addr == self.node.GetAddr() {
			isOwner = true
		}
	}
	return
}
func (self *Node) clean() {
	deleted := 0
	put := 0
	if nextKey, existed := self.circularNext(self.node.GetPosition()); existed {
		if owners, isOwner := self.owners(nextKey); !isOwner {
			var sync *radix.Sync
			for index, owner := range owners {
				sync = radix.NewSync(self.tree, (remoteHashTree)(owner)).From(nextKey).To(owners[0].Pos)
				if index == len(owners)-2 {
					sync.Destroy()
				}
				sync.Run()
				deleted += sync.DelCount()
				put += sync.PutCount()
			}
		}
		if deleted != 0 || put != 0 {
			self.lock.RLock()
			defer self.lock.RUnlock()
			for _, l := range self.cleanListeners {
				l(self, deleted, put)
			}
		}
	}
}
func (self *Node) MustStart() *Node {
	if err := self.Start(); err != nil {
		panic(err)
	}
	return self
}
func (self *Node) MustJoin(addr string) {
	self.timer.Conform(remotePeer(common.Remote{Addr: addr}))
	self.node.MustJoin(addr)
}
func (self *Node) Time() time.Time {
	return time.Unix(0, self.timer.ContinuousTime())
}
func (self *Node) Owned() int {
	pred := self.node.GetPredecessor()
	me := self.node.Remote()
	cmp := bytes.Compare(pred.Pos, me.Pos)
	if cmp < 0 {
		return self.tree.RealSizeBetween(pred.Pos, me.Pos, true, false)
	} else if cmp > 0 {
		return self.tree.RealSizeBetween(pred.Pos, nil, true, false) + self.tree.RealSizeBetween(nil, me.Pos, true, false)
	}
	if pred.Less(me) {
		return 0
	}
	return self.tree.RealSize()
}
