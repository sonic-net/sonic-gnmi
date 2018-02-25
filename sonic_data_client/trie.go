package client

import (
	"strings"
)

// Trie implmentation is adpated from https://github.com/derekparker/trie/blob/master/trie.go
type Node struct {
	val       string
	depth     int
	term      bool
	wildcards map[string]*Node //if not empty, it has wildcards children
	children  map[string]*Node
	parent    *Node
	meta      interface{}
}

type Trie struct {
	root *Node
	size int
}

// Creates a new v2r Trie with an initialized root Node.
func NewTrie() *Trie {
	return &Trie{
		root: &Node{children: make(map[string]*Node), wildcards: make(map[string]*Node), depth: 0},
		size: 0,
	}
}

// Returns the root node for the Trie.
func (t *Trie) Root() *Node {
	return t.root
}

// Adds the key to the Trie, including meta data. Meta data
// is stored as `interface{}` and must be type cast by
// the caller.
func (t *Trie) Add(keys []string, meta interface{}) *Node {
	t.size++

	node := t.root
	for _, key := range keys {
		if n, ok := node.children[key]; ok {
			node = n
		} else {
			node = node.NewChild(key, nil, false)
		}
	}
	node = node.NewChild("", meta, true)
	return node
}

// Removes a key from the trie
func (t *Trie) Remove(keys []string) {
	var (
		i    int
		rs   = keys
		node = findNode(t.Root(), keys)
	)

	t.size--
	for n := node.Parent(); n != nil; n = n.Parent() {
		i++
		if len(n.Children()) > 1 {
			r := rs[len(rs)-i]
			n.RemoveChild(r)
			break
		}
	}
}

// Finds and returns node associated
// with `key`.
func (t *Trie) Find(keys []string) (*Node, bool) {
	node := findNode(t.Root(), keys)
	if node == nil {
		return nil, false
	}

	node, ok := node.Children()[""]
	if !ok || !node.term {
		return nil, false
	}

	return node, true
}

// Creates and returns a pointer to a new child for the node.
func (n *Node) NewChild(val string, meta interface{}, term bool) *Node {
	node := &Node{
		val:       val,
		term:      term,
		meta:      meta,
		parent:    n,
		children:  make(map[string]*Node),
		wildcards: make(map[string]*Node),
		depth:     n.depth + 1,
	}
	n.children[val] = node
	if strings.HasSuffix(val, "*") {
		n.wildcards[val] = node
	}
	return node
}

func (n *Node) RemoveChild(r string) {
	delete(n.children, r)
	if _, ok := n.wildcards[r]; ok {
		delete(n.wildcards, r)
	}
}

// Returns the parent of this node.
func (n Node) Parent() *Node {
	return n.parent
}

// Returns the meta information of this node.
func (n Node) Meta() interface{} {
	return n.meta
}

// Returns the children of this node.
func (n Node) Children() map[string]*Node {
	return n.children
}

func (n Node) Val() string {
	return n.val
}

func findNode(node *Node, keys []string) *Node {
	if node == nil {
		return nil
	}

	if len(keys) == 0 {
		return node
	}

	n, ok := node.Children()[keys[0]]
	if !ok {
		var val string
		for val, n = range node.wildcards {
			if strings.HasPrefix(keys[0], val[:len(val)-1]) {
				ok = true
				break
			}
		}
		if !ok {
			return nil
		}
	}

	var nkeys []string
	if len(keys) > 1 {
		nkeys = keys[1:]
	}

	return findNode(n, nkeys)
}
