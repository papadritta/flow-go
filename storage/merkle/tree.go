// (c) 2019 Dapper Labs - ALL RIGHTS RESERVED

package merkle

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/onflow/flow-go/ledger/common/bitutils"
	"golang.org/x/crypto/blake2b"
)

var (
	ErrorIncompatibleKeyLength = errors.New("key has incompatible size")
)

// maxKeyLength in bytes:
// For any key, we need to ensure that the entire path can be stored in a short node.
// A short node stores the _number of bits_ for the path segment it represents in 2 bytes.
// However, a short node with zero path length is not part of our storage model. Therefore,
// we use the convention:
//  * for path length l with 1 ≤ l ≤ 65535: we represent l as unsigned int with big-endian encoding
//  * for l = 65536: we represent l as binary 00000000 00000000
// This convention organically utilizes the natural occurring overflow and is therefore extremely
// efficient. In summary, we are able to represent key length of up to 65536 bits, i.e. 8192 bytes.
const maxKeyLength = 8192

// Tree represents a binary patricia merkle tree. The difference with a normal
// merkle tree is that it compresses paths that lead to a single leaf into a
// single intermediary node, which makes it significantly more space-efficient
// and a lot harder to exploit for denial-of-service attacks. On the downside,
// it makes insertions and deletions more complex, as we need to split nodes
// and merge them, depending on whether there are leaves or not.
//
// CONVENTION:
//  * If the tree contains _any_ elements, the tree is defined by its root vertex.
//    This case follows completely the convention for nodes: "In any existing tree,
//    all nodes are non-nil."
//  * Without any stored elements, there exists no root vertex in this data model,
//    and we set `root` to nil.
type Tree struct {
	keyLength int
	root      node
}

// NewTree creates a new empty patricia merkle tree, with keys of the given
// `keyLength` (length measured in bytes).
// The current implementation only works with 1 ≤ keyLength ≤ 8192. Otherwise,
// the sentinel error `ErrorIncompatibleKeyLength` is returned.
func NewTree(keyLength int) (*Tree, error) {
	if keyLength < 1 || maxKeyLength < keyLength {
		return nil, fmt.Errorf("key length %d is outside of supported interval [1, %d]: %w", keyLength, maxKeyLength, ErrorIncompatibleKeyLength)
	}
	return &Tree{
		keyLength: keyLength,
		root:      nil,
	}, nil
}

// Put stores the given value in the trie under the given key. If the key
// already exists, it will replace the value and return true. All inputs
// are internally stored and copied where necessary, thereby allowing
// external code to re-use the slices.
// Returns:
//  * (false, nil): key-value pair is stored; key did _not_ yet exist prior to update
//  * (true, nil):  key-value pair is stored; key existed prior to update and the old
//                  value was overwritten
//  * (false, error): with possible error returns
//    - ErrorIncompatibleKeyLength if `key` has different length than the pre-configured value
//    No other errors are returned.
func (t *Tree) Put(key []byte, val []byte) (bool, error) {
	if len(key) != t.keyLength {
		return false, fmt.Errorf("trie is configured for key length of %d bytes, but got key with length %d: %w", t.keyLength, len(key), ErrorIncompatibleKeyLength)
	}
	replaced := t.unsafePut(key, val)
	return replaced, nil
}

// unsafePut stores the given value in the trie under the given key. If the
// key already exists, it will replace the value and return true.
// UNSAFE:
//  * all keys must have identical lengths, which is not checked here.
func (t *Tree) unsafePut(key []byte, val []byte) bool {
	// the path through the tree is determined by the key; we decide whether to
	// go left or right based on whether the next bit is set or not

	// we use a pointer that points at the current node in the tree
	cur := &t.root

	// we use an index to keep track of the bit we are currently looking at
	index := 0

	// the for statement keeps running until we reach a leaf in the merkle tree
	// if the leaf is nil, it was empty and we insert a new value
	// if the leaf is a valid pointer, we overwrite the previous value
PutLoop:
	for {
		switch n := (*cur).(type) {

		// if we have a full node, we have a node on each side to go to, so we
		// just pick the next node based on whether the bit is set or not
		case *full:
			// if the bit is 0, we go left; otherwise (bit value 1), we go right
			if bitutils.ReadBit(key, index) == 0 {
				cur = &n.left
			} else {
				cur = &n.right
			}

			// we forward the index by one to look at the next bit
			index++

			continue PutLoop

		// if we have a short node, we have a path of several bits to the next
		// node; in that case, we use as much of the shared path as possible
		case *short:
			// first, we find out how many bits we have in common
			commonCount := 0
			shortPathCount := n.count
			for i := 0; i < shortPathCount; i++ {
				if bitutils.ReadBit(key, i+index) != bitutils.ReadBit(n.path, i) {
					break
				}
				commonCount++
			}

			// if the common and node count are equal, we share all of the path
			// we can simply forward to the child of the short node and continue
			if commonCount == shortPathCount {
				cur = &n.child
				index += commonCount
				continue PutLoop
			}

			// if the common count is non-zero, we share some of the path;
			// first, we insert a common short node for the shared path
			if commonCount > 0 {
				commonPath := bitutils.MakeBitVector(commonCount)
				for i := 0; i < commonCount; i++ {
					bitutils.WriteBit(commonPath, i, bitutils.ReadBit(key, i+index))
				}
				commonNode := &short{count: commonCount, path: commonPath}
				*cur = commonNode
				cur = &commonNode.child
				index = index + commonCount
			}

			// we then insert a full node that splits the tree after the shared
			// path; we set our pointer to the side that lies on our path,
			// and use a remaining pointer for the other side of the node
			var remain *node
			splitNode := &full{}
			*cur = splitNode
			if bitutils.ReadBit(n.path, commonCount) == 1 {
				cur = &splitNode.left
				remain = &splitNode.right
			} else {
				cur = &splitNode.right
				remain = &splitNode.left
			}
			index++

			// we can continue our insertion at this point, but we should first
			// insert the correct node on the other side of the created full
			// node; if we have remaining path, we create a short node and
			// forward to its path; finally, we set the leaf to original leaf
			remainCount := n.count - commonCount - 1
			if remainCount > 0 {
				remainPath := bitutils.MakeBitVector(remainCount)
				for i := 0; i < remainCount; i++ {
					bitutils.WriteBit(remainPath, i, bitutils.ReadBit(n.path, i+commonCount+1))
				}
				remainNode := &short{count: remainCount, path: remainPath}
				*remain = remainNode
				remain = &remainNode.child
			}
			*remain = n.child

			continue PutLoop

		// if we have a leaf node, we reached a non-empty leaf
		case *leaf:
			n.val = append(make([]byte, 0, len(val)), val...)
			return true // return true to indicate that we overwrote

		// if we have nil, we reached the end of any shared path
		case nil:
			// if we have reached the end of the key, insert the new value
			totalCount := len(key) * 8
			if index == totalCount {
				// Instantiate a new leaf holding a _copy_ of the provided key-value pair,
				// to protect the slices from external modification.
				*cur = &leaf{
					val: append(make([]byte, 0, len(val)), val...),
				}
				return false
			}

			// otherwise, insert a short node with the remainder of the path
			finalCount := totalCount - index
			finalPath := bitutils.MakeBitVector(finalCount)
			for i := 0; i < finalCount; i++ {
				bitutils.WriteBit(finalPath, i, bitutils.ReadBit(key, index+i))
			}
			finalNode := &short{count: finalCount, path: []byte(finalPath)}
			*cur = finalNode
			cur = &finalNode.child
			index += finalCount

			continue PutLoop
		}
	}
}

// Get will retrieve the value associated with the given key. It returns true
// if the key was found and false otherwise.
func (t *Tree) Get(key []byte) ([]byte, bool) {
	if t.root == nil || t.keyLength != len(key) {
		return nil, false
	}
	return t.unsafeGet(key)
}

// unsafeGet retrieves the value associated with the given key. It returns true
// if the key was found and false otherwise.
// UNSAFE:
//  * all keys must have identical lengths, which is not checked here.
func (t *Tree) unsafeGet(key []byte) ([]byte, bool) {
	cur := &t.root // start at the root
	index := 0     // and we start at a zero index in the path

GetLoop:
	for {
		switch n := (*cur).(type) {

		// if we have a full node, we can follow the path for at least one more
		// bit, so go left or right depending on whether it's set or not
		case *full:
			// forward pointer and index to the correct child
			if bitutils.ReadBit(key, index) == 0 {
				cur = &n.left
			} else {
				cur = &n.right
			}

			index++
			continue GetLoop

		// if we have a short path, we can only follow the short node if
		// its paths has all bits in common with the key we are retrieving
		case *short:
			// if any part of the path doesn't match, key doesn't exist
			for i := 0; i < n.count; i++ {
				if bitutils.ReadBit(key, i+index) != bitutils.ReadBit(n.path, i) {
					return nil, false
				}
			}

			// forward pointer and index to child
			cur = &n.child
			index += n.count

			continue GetLoop

		// if we have a leaf, we found the key, return value and true
		case *leaf:
			return n.val, true

		// if we have a nil node, key doesn't exist, return nil and false
		case nil:
			return nil, false
		}
	}
}

// Prove constructs inclusion proof for a given key if the key exists in the trie.
// it traverse the trie from top to down and collect data for proof as follows:
//  - if full node, capture the sibling node hash value and append zero to short counts
//  - if short node, appends the node.shortCount to the short count list
//  - if leaf, would capture the hash of the value
func (t *Tree) Prove(key []byte) (*Proof, bool) {

	// we start at the root again
	cur := &t.root

	// we use the given key as path again
	path := bitutils.MakeBitVector(t.keyLength)
	for i := 0; i < t.keyLength; i++ {
		if bitutils.ReadBit(key, i) == 1 {
			bitutils.SetBit(path, i)
		}
	}

	// and we start at a zero index in the path
	index := 0

	// init proof params
	hashValues := make([][]byte, 0)
	shortCounts := make([]uint8, 0)

ProveLoop:
	for {
		switch n := (*cur).(type) {

		// if we have a full node, we can follow the path for at least one more
		// bit, so go left or right depending on whether it's set or not
		case *full:
			var neighbour node
			// forward pointer and index to the correct child
			if bitutils.ReadBit(key, index) == 0 {
				neighbour = n.right
				cur = &n.left
			} else {
				neighbour = n.left
				cur = &n.right
			}

			// capturing short count as zero hints that we had a full node
			// so we can read a hashValue from hashValues
			shortCounts = append(shortCounts, 0)
			hashValues = append(hashValues, neighbour.Hash())

			index++
			continue ProveLoop

		// if we have a short path, we can only follow the path if we have all
		// of the short node path in common with the key
		case *short:

			// if any part of the path doesn't match, key doesn't exist
			for i := 0; i < n.count; i++ {
				if bitutils.ReadBit(key, i+index) != bitutils.ReadBit(n.path, i) {
					return nil, false
				}
			}

			// capturing a non-zero short counts hints that we had a short node
			// during traverse and also capturing is needed to compute hash value
			// for a short node.
			shortCounts = append(shortCounts, uint8(n.count))

			// forward pointer and index to child
			cur = &n.child
			index += n.count

			continue ProveLoop

		// if we have a leaf, we found the key, return value and true
		case *leaf:
			return &Proof{
				Key:           key[:],
				HashValue:     n.Hash(),
				ShortCounts:   shortCounts,
				InterimHashes: hashValues,
			}, true

		// if we have a nil node, key doesn't exist, return nil and false
		case nil:
			return nil, false
		}
	}
}

// Del removes the value associated with the given key from the patricia
// merkle trie. It returns true if they key was found and false otherwise.
// Internally, any parent nodes between the leaf up to the closest shared path
// will be deleted or merged, which keeps the trie deterministic regardless of
// insertion and deletion orders.
func (t *Tree) Del(key []byte) bool {
	if t.root == nil || t.keyLength != len(key) {
		return false
	}
	return t.unsafeDel(key)
}

// unsafeDel removes the value associated with the given key from the patricia
// merkle trie. It returns true if they key was found and false otherwise.
// Internally, any parent nodes between the leaf up to the closest shared path
// will be deleted or merged, which keeps the trie deterministic regardless of
// insertion and deletion orders.
// UNSAFE:
//  * all keys must have identical lengths, which is not checked here.
func (t *Tree) unsafeDel(key []byte) bool {
	cur := &t.root // start at the root
	index := 0     // the index points to the bit we are processing in the path

	// we initialize three pointers pointing to a dummy empty node
	// this is used to keep track of the node we last pointed to, as well as
	// its parent and grand parent, which is needed in case we remove a full
	// node and have to merge several other nodes into a short node; otherwise,
	// we would not keep the tree as compact as possible, and it would no longer
	// be deterministic after deletes
	dummy := node(&dummy{})
	last, parent, grand := &dummy, &dummy, &dummy

DelLoop:
	for {
		switch n := (*cur).(type) {

		// if we have a full node, we forward all of the pointers
		case *full:
			// keep track of grand-parent, parent and node for cleanup
			grand = parent
			parent = last
			last = cur

			// forward pointer and index to the correct child
			if bitutils.ReadBit(key, index) == 0 {
				cur = &n.left
			} else {
				cur = &n.right
			}

			index++
			continue DelLoop

		// if we have a short node, we forward by all of the common path if
		// possible; otherwise the node wasn't found
		case *short:
			// keep track of grand-parent, parent and node for cleanup
			grand = parent
			parent = last
			last = cur

			// if the path doesn't match at any point, we can't find the node
			for i := 0; i < n.count; i++ {
				if bitutils.ReadBit(key, i+index) != bitutils.ReadBit(n.path, i) {
					return false
				}
			}

			// forward pointer and index to the node child
			cur = &n.child
			index += n.count

			continue DelLoop

		// if we have a leaf node, we remove it and continue with cleanup
		case *leaf:
			*cur = nil // replace the current pointer with nil to delete the node
			break DelLoop

		// if we reach nil, the node doesn't exist
		case nil:
			return false
		}
	}

	// if the last node before reaching the leaf is a short node, we set it to
	// nil to remove it from the tree and move the pointer to its parent
	_, ok := (*last).(*short)
	if ok {
		*last = nil
		last = parent
		parent = grand
	}

	// if the last node here is not a full node, we are done; we never have two
	// short nodes in a row, which means we have reached the root
	f, ok := (*last).(*full)
	if !ok {
		return true
	}

	// if the last node is a full node, we need to convert it into a short node
	// that holds the undeleted child and the corresponding bit as path
	var n *short
	newPath := bitutils.MakeBitVector(1)
	if f.left != nil {
		bitutils.ClearBit(newPath, 0)
		n = &short{count: 1, path: newPath, child: f.left}
	} else {
		bitutils.SetBit(newPath, 0)
		n = &short{count: 1, path: newPath, child: f.right}
	}
	*last = n

	// if the child is also a short node, we have to merge them and use the
	// child's child as the child of the merged short node
	c, ok := n.child.(*short)
	if ok {
		merge(n, c)
	}

	// if the parent is also a short node, we have to merge them and use the
	// current child as the child of the merged node
	p, ok := (*parent).(*short)
	if ok {
		merge(p, n)
	}

	// NOTE: if neither the parent nor the child are short nodes, we simply
	// bypass both conditional scopes and land here right away
	return true
}

// Hash returns the root hash of this patricia merkle tree.
// Per convention, an empty trie has an empty hash.
func (t *Tree) Hash() []byte {
	if t.root == nil {
		return []byte{}
	}
	return t.root.Hash()
}

// merge will merge a child short node into a parent short node.
func merge(p *short, c *short) {
	totalCount := p.count + c.count
	totalPath := bitutils.MakeBitVector(totalCount)
	for i := 0; i < p.count; i++ {
		bitutils.WriteBit(totalPath, i, bitutils.ReadBit(p.path, i))
	}
	for i := 0; i < c.count; i++ {
		bitutils.WriteBit(totalPath, i+p.count, bitutils.ReadBit(c.path, i))
	}
	p.count = totalCount
	p.path = totalPath
	p.child = c.child
}

type Proof struct {
	Key           []byte   // key
	HashValue     []byte   // hash of the Value
	ShortCounts   []uint8  // if set to one means full node, else means short node
	InterimHashes [][]byte // hash values
}

// Verify returns if the proof is valid and false otherwise
func (p *Proof) Verify(expectedRootHash []byte) bool {
	// iterate backward and verify the proof
	currentHash := p.HashValue
	hashIndex := len(p.InterimHashes) - 1

	// compute last path index
	pathIndex := len(p.InterimHashes)
	for _, sc := range p.ShortCounts {
		pathIndex += int(sc)
	}

	for i := len(p.ShortCounts) - 1; i >= 0; i-- {
		shortCounts := p.ShortCounts[i]
		if shortCounts == 0 { // is full node
			neighbour := p.InterimHashes[hashIndex]
			hashIndex--
			pathIndex--
			h, _ := blake2b.New256(fullNodeTag) // blake2b.New256(..) error for given MAC (verified in tests)
			// based on the bit on pathIndex, compute the hash
			if bitutils.ReadBit(p.Key, pathIndex) == 1 {
				_, _ = h.Write(neighbour)
				_, _ = h.Write(currentHash)
				currentHash = h.Sum(nil)
			} else {
				_, _ = h.Write(currentHash)
				_, _ = h.Write(neighbour)
				currentHash = h.Sum(nil)
			}
			continue
		}
		// else its a short node
		// construct the common path
		commonPath := bitutils.MakeBitVector(int(shortCounts))
		pathIndex = pathIndex - int(shortCounts)
		for j := 0; j < int(shortCounts); j++ {
			if bitutils.ReadBit(p.Key, pathIndex+j) == 1 {
				bitutils.SetBit(commonPath, j)
			}
		}

		h, _ := blake2b.New256(shortNodeTag) // blake2b.New256(..) error for given MAC (verified in tests)
		c := serializedPathSegmentLength(int(shortCounts))
		_, _ = h.Write(c[:])        // blake2b.Write(..) never errors for _any_ input
		_, _ = h.Write(commonPath)  // blake2b.Write(..) never errors for _any_ input
		_, _ = h.Write(currentHash) // blake2b.Write(..) never errors for _any_ input
		currentHash = h.Sum(nil)
	}

	if pathIndex != 0 || !bytes.Equal(currentHash, expectedRootHash) {
		return false
	}
	return true
}
