package pathz_authorizer

import (
	"fmt"
	"os"
	"sort"
	"sync"

	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	pathzpb "github.com/openconfig/gnsi/pathz"
)

const (
	wildCard = "*"
)

var exists = struct{}{}

type stringSet map[string]interface{}

// PrintPathWithPrefix returns the string represtation of a path.
func PrintPathWithPrefix(prefix, path *gnmipb.Path) string {
	netPath := []*gnmipb.PathElem{}
	netPath = append(netPath, prefix.GetElem()...)
	netPath = append(netPath, path.GetElem()...)
	return printPath(netPath)
}

func printPath(path []*gnmipb.PathElem) string {
	ret := ""
	for _, e := range path {
		ret += "/" + e.GetName()
		if len(e.GetKey()) != 0 {
			// We sort all keys in alphabetical order.
			keys := []string{}
			for k := range e.GetKey() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				ret += "[" + k + "=" + e.GetKey()[k] + "]"
			}
		}
	}
	if ret == "" {
		ret = "/"
	}
	return ret
}

// Result stores the gNMI authorization result.
type Result struct {
	// Action can be pathzpb.Action_ACTION_UNSPECIFIED, which indicates that no matching
	// rule is found. Callers should apply deny action if no rule is
	// matched.
	Action pathzpb.Action
	// The matched configuration rule ID.
	// This field is empty if there is no matched rule.
	RuleId string
	// The matched configuration rule.
	// This field is the gNMI path in the configuration rule in string format.
	// This field is empty if there is no matched rule.
	MatchedRule string
}

type ruleAction struct {
	ruleId string
	action pathzpb.Action
}

// Read, subscribe, and write permissions are independently configured.
// It is possible that a user have write permission but not read permission on
// the same gNMI path.
type permission struct {
	read  ruleAction
	write ruleAction
}

// A node can be a leaf node, name node, or a key node.
// A leaf node only contains permision info. There is no next node.
// A name node is used to look up the next name field in gNMI PathElem.
// A key node is used to look up the next key field in gNMI PathElem.
type gnmiAuthzNode struct {
	users  map[string]permission
	groups map[string]permission
	// The string representation of the gNMI path in the rule.
	rule string
	// The nameNext field stores the next nodes for all the next path names
	// configured in the rule.
	nameNext map[string]*gnmiAuthzNode
	// The key field stores the next key name. (All keys must be sorted in
	// alphabetical order in gNMI PathElem.) If the key field is not empty, the
	// keyNext field should not be empty.
	key string
	// The keyNext field stores the next nodes for all the key values configured
	// in the rule. The "*" character is a wild card.
	keyNext map[string]*gnmiAuthzNode
}

// GnmiAuthzProcessor performs the main gNMI authorization logic.
type GnmiAuthzProcessor struct {
	groups map[string]stringSet
	root   *gnmiAuthzNode
	policy *pathzpb.AuthorizationPolicy
	mux    sync.Mutex
}

// GnmiAuthzProcessorInterface defines the gNMI authorization processor
// interface.
type GnmiAuthzProcessorInterface interface {
	// Given a user, a gNMI path, and the access mode (read or write), returns
	// the authorization result.
	// It is recommended to input leaf paths for correct authorization result.
	// If a subtree path is passed, detailed rules under the subtree will not
	// be matched.
	Authorize(user string, path *gnmipb.Path, mode pathzpb.Mode) (*Result, error)

	// Same as Authorize, with a path prefix.
	AuthorizeWithPrefix(user string, prefix, path *gnmipb.Path, mode pathzpb.Mode) (*Result, error)

	// Given the policy file in proto text format, update the authorization
	// policy. The processor will start with an empty policy that denies all
	// access. If error is returned, the processor's current policy will not
	// change.
	UpdatePolicyFromFile(policyFile string) error

	// Same as UpdatePolicyFromFile, but takes a proto instead of a file name.
	UpdatePolicyFromProto(policyProto *pathzpb.AuthorizationPolicy) error

	// Return the current policy.
	// Return nil if no policy has been successfully configured, in such case
	// the processor will deny all access.
	GetPolicy() *pathzpb.AuthorizationPolicy
}

func (result *Result) updateResult(p permission, mode pathzpb.Mode, rule string) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	// If the result is already DENY, we don't update it.
	if result.Action == pathzpb.Action_ACTION_DENY {
		return nil
	}
	// Read and write permissions are checked independently.
	// It is possible that a user have write permission but not read permission
	// on the same gNMI path.
	var access ruleAction
	switch mode {
	case pathzpb.Mode_MODE_READ:
		access = p.read
	case pathzpb.Mode_MODE_WRITE:
		access = p.write
	default:
		return fmt.Errorf("invalid mode")
	}
	if access.action != pathzpb.Action_ACTION_UNSPECIFIED {
		result.Action = access.action
		result.RuleId = access.ruleId
		result.MatchedRule = rule
	}
	return nil
}

func (result *Result) logResult(user string, path *gnmipb.Path, mode pathzpb.Mode) {
	modeStr := "read"
	if mode == pathzpb.Mode_MODE_WRITE {
		modeStr = "write"
	}
	// Always log denied cases.
	if result.Action == pathzpb.Action_ACTION_UNSPECIFIED {
		log.V(2).Infof("User %s with %s request on %s does not match any gNMI ACL rule. Request denied.", user, modeStr, printPath(path.GetElem()))
	} else if result.Action == pathzpb.Action_ACTION_DENY {
		log.V(2).Infof("User %s with %s request on %s matched gNMI ACL rule %s (rule ID: %s). Request denied.", user, modeStr, printPath(path.GetElem()), result.MatchedRule, result.RuleId)
	}
}

func (p *permission) updatePermission(a pathzpb.Action, m pathzpb.Mode, ruleId string) error {
	if p == nil {
		return fmt.Errorf("permission cannot be nil")
	}
	if a == pathzpb.Action_ACTION_UNSPECIFIED {
		return nil
	}
	var r *ruleAction
	switch m {
	case pathzpb.Mode_MODE_UNSPECIFIED:
		return nil
	case pathzpb.Mode_MODE_READ:
		r = &p.read
	case pathzpb.Mode_MODE_WRITE:
		r = &p.write
	default:
		return fmt.Errorf("invalid mode")
	}
	// If multiple rules configure the same path with both permit and
	// deny actions, we will honor the deny rule.
	if r.action == pathzpb.Action_ACTION_DENY {
		return nil
	}
	r.action = a
	r.ruleId = ruleId
	return nil
}

func (node *gnmiAuthzNode) authorize(user string, path []*gnmipb.PathElem, mode pathzpb.Mode, nameIdx int, keys []string, keyIdx int, groups map[string]stringSet) Result {
	ret := Result{Action: pathzpb.Action_ACTION_UNSPECIFIED}
	if node == nil {
		return ret
	}

	if permission, ok := node.users[user]; ok {
		ret.updateResult(permission, mode, node.rule)
	}

	// Not going to check the group rules if the user is in the user rules.
	if ret.Action == pathzpb.Action_ACTION_UNSPECIFIED || ret.MatchedRule == "" {
		for group, permission := range node.groups {
			if members, ok := groups[group]; ok {
				if _, ok := members[user]; ok {
					ret.updateResult(permission, mode, node.rule)
					// A user can be in multiple groups, we check all groups
					// unless the result is DENY.
					if ret.Action == pathzpb.Action_ACTION_DENY && ret.MatchedRule != "" {
						break
					}
				}
			}
		}
	}

	// Return if it is the end of the path.
	if nameIdx >= len(path) {
		return ret
	}

	// In the process of key lookup.
	if len(keys) > 0 {
		// Key index should not go out of range.
		if keyIdx >= len(keys) {
			log.V(0).Infof("Key index got out of range for key list %v", keys)
			return ret
		}

		// Key value mismatches.
		if keys[keyIdx] != node.key {
			log.V(0).Infof("Key value mismatches. Expect %v, got %v", node.key, keys[keyIdx])
			return ret
		}

		// Key name does not exist in path.
		keyValue, ok := path[nameIdx].GetKey()[node.key]
		if !ok {
			log.V(0).Infof("Key name %v does not exist in path", node.key)
			return ret
		}

		nextNameIdx := nameIdx
		nextKeys := keys
		nextKeyIdx := keyIdx + 1

		// Exit key lookup process if it reaches the end of the key list.
		if nextKeyIdx == len(keys) {
			nextNameIdx = nameIdx + 1
			nextKeys = nil
			nextKeyIdx = 0
		}

		// First, we will check exact match.
		if nextNode, ok := node.keyNext[keyValue]; ok {
			// Return if we get a hit in exact key match. We will NOT check
			// wild card rule even if it might match longer prefix. Notice
			// that the keys are stored in alphabetical order if there are
			// multiple keys. We will return the first one that got matched.
			r := nextNode.authorize(user, path, mode, nextNameIdx, nextKeys, nextKeyIdx, groups)
			if r.Action != pathzpb.Action_ACTION_UNSPECIFIED && r.MatchedRule != "" {
				return r
			}
		}

		// Then, check wildcard.
		if nextNode, ok := node.keyNext[wildCard]; ok {
			r := nextNode.authorize(user, path, mode, nextNameIdx, nextKeys, nextKeyIdx, groups)
			if r.Action != pathzpb.Action_ACTION_UNSPECIFIED && r.MatchedRule != "" {
				return r
			}
		}

		return ret
	}

	if nextNode, ok := node.nameNext[path[nameIdx].GetName()]; ok {
		nextNameIdx := nameIdx + 1
		var nextKeys []string

		// Check if it needs to perform key lookup process.
		if len(path[nameIdx].GetKey()) > 0 {
			nextNameIdx = nameIdx
			nextKeys = []string{}
			// Build a list of stored keys.
			for k := range path[nameIdx].GetKey() {
				nextKeys = append(nextKeys, k)
			}
			sort.Strings(nextKeys)
		}

		r := nextNode.authorize(user, path, mode, nextNameIdx, nextKeys, 0, groups)
		if r.Action != pathzpb.Action_ACTION_UNSPECIFIED && r.MatchedRule != "" {
			return r
		}
	}

	return ret
}

func (node *gnmiAuthzNode) insertPath(rule *pathzpb.AuthorizationRule, nameIdx int, keys []string, keyIdx int) error {
	if node == nil {
		return fmt.Errorf("node cannot be nil")
	}

	// Building a leaf node.
	if nameIdx >= len(rule.GetPath().GetElem()) {
		node.rule = printPath(rule.GetPath().GetElem())
		switch rule.GetPrincipal().(type) {
		case *pathzpb.AuthorizationRule_User:
			if node.users == nil {
				node.users = map[string]permission{}
			}
			_, ok := node.users[rule.GetUser()]
			if !ok {
				node.users[rule.GetUser()] = permission{
					read:  ruleAction{action: pathzpb.Action_ACTION_UNSPECIFIED},
					write: ruleAction{action: pathzpb.Action_ACTION_UNSPECIFIED},
				}
			}
			p := node.users[rule.GetUser()]
			p.updatePermission(rule.GetAction(), rule.GetMode(), rule.GetId())
			node.users[rule.GetUser()] = p
		case *pathzpb.AuthorizationRule_Group:
			if node.groups == nil {
				node.groups = map[string]permission{}
			}
			_, ok := node.groups[rule.GetGroup()]
			if !ok {
				node.groups[rule.GetGroup()] = permission{
					read:  ruleAction{action: pathzpb.Action_ACTION_UNSPECIFIED},
					write: ruleAction{action: pathzpb.Action_ACTION_UNSPECIFIED},
				}
			}
			p := node.groups[rule.GetGroup()]
			p.updatePermission(rule.GetAction(), rule.GetMode(), rule.GetId())
			node.groups[rule.GetGroup()] = p
		default:
			return fmt.Errorf("invalid principal type")
		}
		return nil
	}

	// Check if it is building a key node
	if len(keys) > 0 {
		if keyIdx >= len(keys) {
			return fmt.Errorf("key index out of range")
		}
		v, ok := rule.GetPath().GetElem()[nameIdx].GetKey()[keys[keyIdx]]
		if !ok {
			return fmt.Errorf("key %v not found in path", keys[keyIdx])
		}
		if node.key != "" && node.key != keys[keyIdx] {
			return fmt.Errorf("key %v mismatch from other configured rule", node.key)
		}
		node.key = keys[keyIdx]
		if node.keyNext == nil {
			node.keyNext = map[string]*gnmiAuthzNode{}
		}
		if _, ok := node.keyNext[v]; !ok {
			node.keyNext[v] = &gnmiAuthzNode{}
		}
		nextNameIdx := nameIdx
		nextKeys := keys
		nextKeyIdx := keyIdx + 1
		// Exit key node if it reaches the end of the key list.
		if nextKeyIdx == len(keys) {
			nextNameIdx = nameIdx + 1
			nextKeys = nil
			nextKeyIdx = 0
		}
		return node.keyNext[v].insertPath(rule, nextNameIdx, nextKeys, nextKeyIdx)
	}

	// Building a name node.
	if node.nameNext == nil {
		node.nameNext = map[string]*gnmiAuthzNode{}
	}
	name := rule.GetPath().GetElem()[nameIdx].GetName()
	if _, ok := node.nameNext[name]; !ok {
		node.nameNext[name] = &gnmiAuthzNode{}
	}
	nextNameIdx := nameIdx + 1
	var nextKeys []string
	// Check if it needs to switch to key node.
	if len(rule.GetPath().GetElem()[nameIdx].GetKey()) > 0 {
		nextNameIdx = nameIdx
		nextKeys = []string{}
		// Build a list of stored keys.
		for k := range rule.GetPath().GetElem()[nameIdx].GetKey() {
			nextKeys = append(nextKeys, k)
		}
		sort.Strings(nextKeys)
	}
	return node.nameNext[name].insertPath(rule, nextNameIdx, nextKeys, 0)
}

// Authorize is a GnmiAuthzProcessorInterface method.
func (processor *GnmiAuthzProcessor) Authorize(user string, path *gnmipb.Path, mode pathzpb.Mode) (*Result, error) {
	if processor == nil {
		log.V(0).Info("Authorize error: nil pointer")
		return nil, fmt.Errorf("processor cannot be nil")
	}
	if mode == pathzpb.Mode_MODE_UNSPECIFIED {
		log.V(0).Infof("Authorize error: undefined access mode")
		return nil, fmt.Errorf("mode must be read or write")
	}

	processor.mux.Lock()
	defer processor.mux.Unlock()
	r := processor.root.authorize(user, path.GetElem(), mode, 0, nil, 0, processor.groups)
	r.logResult(user, path, mode)
	return &r, nil
}

// Authorize is a GnmiAuthzProcessorInterface method.
func (processor *GnmiAuthzProcessor) AuthorizeWithPrefix(user string, prefix, path *gnmipb.Path, mode pathzpb.Mode) (*Result, error) {
	if processor == nil {
		log.V(0).Info("Authorize error: nil pointer")
		return nil, fmt.Errorf("processor cannot be nil")
	}
	if mode == pathzpb.Mode_MODE_UNSPECIFIED {
		log.V(0).Infof("Authorize error: undefined access mode")
		return nil, fmt.Errorf("mode must be read or write")
	}
	netPath := []*gnmipb.PathElem{}
	netPath = append(netPath, prefix.GetElem()...)
	netPath = append(netPath, path.GetElem()...)

	processor.mux.Lock()
	defer processor.mux.Unlock()
	r := processor.root.authorize(user, netPath, mode, 0, nil, 0, processor.groups)
	r.logResult(user, path, mode)
	return &r, nil
}

// UpdatePolicyFromFile is a GnmiAuthzProcessorInterface method.
func (processor *GnmiAuthzProcessor) UpdatePolicyFromFile(policyFile string) error {
	if processor == nil {
		log.V(0).Info("UpdatePolicyFromFile error: nil pointer")
		return fmt.Errorf("processor cannot be nil")
	}

	content, err := os.ReadFile(policyFile)
	if err != nil {
		log.V(0).Infof("Failed to open file %s.", policyFile)
		return err
	}
	policy := &pathzpb.AuthorizationPolicy{}
	err = proto.UnmarshalText(string(content), policy)
	if err != nil {
		log.V(0).Infof("Failed to parse file %s.", policyFile)
		return err
	}
	return processor.UpdatePolicyFromProto(policy)
}

// UpdatePolicyFromProto is a GnmiAuthzProcessorInterface method.
func (processor *GnmiAuthzProcessor) UpdatePolicyFromProto(policyProto *pathzpb.AuthorizationPolicy) error {
	if processor == nil {
		log.V(0).Info("UpdatePolicyFromProto error: nil pointer")
		return fmt.Errorf("processor cannot be nil")
	}

	newRoot, newGroups, err := createNewPolicy(policyProto)
	if err != nil {
		log.V(0).Infof("Failed to create new gNMI authorization rule.")
		return err
	}
	processor.mux.Lock()
	defer processor.mux.Unlock()
	processor.root = newRoot
	processor.groups = newGroups
	processor.policy = policyProto
	return nil
}

// GetPolicy is a GnmiAuthzProcessorInterface method.
func (processor *GnmiAuthzProcessor) GetPolicy() *pathzpb.AuthorizationPolicy {
	if processor == nil {
		return nil
	}
	processor.mux.Lock()
	defer processor.mux.Unlock()
	return processor.policy
}

func createGroups(groupsProto []*pathzpb.Group) map[string]stringSet {
	groups := map[string]stringSet{}
	for _, g := range groupsProto {
		if _, ok := groups[g.GetName()]; !ok {
			groups[g.GetName()] = stringSet{}
		}
		for _, u := range g.GetUsers() {
			groups[g.GetName()][u.GetName()] = exists
		}
	}
	return groups
}

func createPolicies(rules []*pathzpb.AuthorizationRule) (*gnmiAuthzNode, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules found")
	}
	root := &gnmiAuthzNode{}
	for _, p := range rules {
		if err := root.insertPath(p, 0, nil, 0); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func createNewPolicy(policyProto *pathzpb.AuthorizationPolicy) (*gnmiAuthzNode, map[string]stringSet, error) {
	rules, err := createPolicies(policyProto.GetRules())
	return rules, createGroups(policyProto.GetGroups()), err
}
