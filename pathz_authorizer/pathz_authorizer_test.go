package pathz_authorizer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	pathzpb "github.com/openconfig/gnsi/pathz"
)

type rule struct {
	path   string
	user   string
	group  string
	action pathzpb.Action
	mode   pathzpb.Mode
}

type group struct {
	name    string
	members []string
}

// Converts a gNMI path from string to proto.
func getGnmiPath(path string) (*gnmipb.Path, error) {
	ret := []*gnmipb.PathElem{}
	elems := strings.Split(path, "/")
	for _, e := range elems {
		if e == "" {
			continue
		}
		pe := &gnmipb.PathElem{}
		kvs := strings.Split(e, "[")
		for i, kv := range kvs {
			if i == 0 {
				pe.Name = kv
				continue
			}
			kvp := strings.Split(kv[:len(kv)-1], "=")
			if len(kvp) != 2 {
				return nil, fmt.Errorf("invalid path elem %v", e)
			}
			if pe.Key == nil {
				pe.Key = map[string]string{}
			}
			pe.Key[kvp[0]] = kvp[1]
		}
		ret = append(ret, pe)
	}
	return &gnmipb.Path{Elem: ret}, nil
}

func getGnmiAuthzConfig(rules []*rule) ([]*pathzpb.AuthorizationRule, error) {
	ret := []*pathzpb.AuthorizationRule{}
	for _, p := range rules {
		ruleId := p.path + "_" + pathzpb.Mode_name[int32(p.mode)] + "_" + pathzpb.Action_name[int32(p.action)]
		path, err := getGnmiPath(p.path)
		if err != nil {
			return nil, err
		}
		if p.user != "" {
			rule := &pathzpb.AuthorizationRule{
				Id:        ruleId + "_user",
				Principal: &pathzpb.AuthorizationRule_User{User: p.user},
				Path:      path,
				Action:    p.action,
				Mode:      p.mode,
			}
			ret = append(ret, rule)
		}
		if p.group != "" {
			rule := &pathzpb.AuthorizationRule{
				Id:        ruleId + "_group",
				Principal: &pathzpb.AuthorizationRule_Group{Group: p.group},
				Path:      path,
				Action:    p.action,
				Mode:      p.mode,
			}
			ret = append(ret, rule)
		}
	}
	return ret, nil
}

func getGroups(groups []*group) []*pathzpb.Group {
	ret := []*pathzpb.Group{}
	for _, g := range groups {
		group := &pathzpb.Group{
			Name:  g.name,
			Users: []*pathzpb.User{},
		}
		for _, m := range g.members {
			group.Users = append(group.Users, &pathzpb.User{Name: m})
		}
		ret = append(ret, group)
	}
	return ret
}

func TestGnsiPathzPolicyConfigError(t *testing.T) {
	rules := []*rule{
		&rule{
			path:   "/a/b[k1=v1][k2=v2]/c",
			user:   "User1",
			group:  "Group1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/b[k1=v1][k3=v3]/c",
			user:   "User1",
			group:  "Group1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
	}
	groups := []*group{
		&group{
			name: "Group1",
			members: []string{
				"User1",
				"User2",
			},
		},
	}
	rs, err := getGnmiAuthzConfig(rules)
	if err != nil {
		t.Errorf("Error in getGnmiAuthzConfig: %v", err)
	}
	processor := &GnmiAuthzProcessor{}
	err = processor.UpdatePolicyFromProto(&pathzpb.AuthorizationPolicy{
		Rules:  rs,
		Groups: getGroups(groups),
	})
	if err == nil {
		t.Errorf("Expect error in UpdatePolicyFromProto, got nil")
	}
}

func TestGnsiPathzPolicyChecker(t *testing.T) {
	rules := []*rule{
		&rule{
			path:   "/",
			user:   "User3",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/b/c",
			user:   "User1",
			group:  "Group1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_WRITE,
		},
		&rule{
			path:   "/a/d[k1=*]/e/f",
			user:   "User1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=*]/e/f",
			group:  "Group1",
			action: pathzpb.Action_ACTION_DENY,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=v1]/e",
			user:   "User1",
			group:  "Group1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=v1]/e",
			user:   "User1",
			action: pathzpb.Action_ACTION_DENY,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=*]/e/g[k2=v2]/h",
			group:  "Group1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=v1]/e/g[k2=*]/h",
			group:  "Group1",
			action: pathzpb.Action_ACTION_DENY,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/d[k1=*]/e/g[k2=v2]/h",
			group:  "Group2",
			action: pathzpb.Action_ACTION_DENY,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/i[k3=*][k4=v4]/j[k5=v5]/k/l",
			user:   "User1",
			action: pathzpb.Action_ACTION_PERMIT,
			mode:   pathzpb.Mode_MODE_READ,
		},
		&rule{
			path:   "/a/i[k3=v3][k4=*]/j[k5=*]/k",
			user:   "User1",
			action: pathzpb.Action_ACTION_DENY,
			mode:   pathzpb.Mode_MODE_READ,
		},
	}
	groups := []*group{
		&group{
			name: "Group1",
			members: []string{
				"User1",
				"User2",
			},
		},
		&group{
			name: "Group2",
			members: []string{
				"User1",
				"User3",
			},
		},
	}
	rs, err := getGnmiAuthzConfig(rules)
	if err != nil {
		t.Errorf("Error in getGnmiAuthzConfig: %v", err)
	}
	processor := &GnmiAuthzProcessor{}
	err = processor.UpdatePolicyFromProto(&pathzpb.AuthorizationPolicy{
		Rules:  rs,
		Groups: getGroups(groups),
	})
	if err != nil {
		t.Errorf("Error in UpdatePolicyFromProto: %v", err)
	}

	for _, tc := range []struct {
		description   string
		user          string
		prefix        string
		path          string
		mode          pathzpb.Mode
		error         bool
		result        pathzpb.Action
		matchedRuleId string
		matchedRule   string
	}{
		{
			description: "Undefined mode",
			user:        "User1",
			path:        "/a/b/c",
			mode:        pathzpb.Mode_MODE_UNSPECIFIED,
			error:       true,
		},
		{
			description: "No matched rule",
			user:        "User3",
			path:        "/a/b/c",
			mode:        pathzpb.Mode_MODE_WRITE,
			error:       false,
			result:      pathzpb.Action_ACTION_UNSPECIFIED,
		},
		{
			description: "No matched prefix rule",
			user:        "User3",
			path:        "/a/d[k1=v1]/e",
			mode:        pathzpb.Mode_MODE_WRITE,
			error:       false,
			result:      pathzpb.Action_ACTION_UNSPECIFIED,
		},
		{
			description:   "Exact path match with no key",
			user:          "User1",
			path:          "/a/b/c",
			mode:          pathzpb.Mode_MODE_WRITE,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/b/c_MODE_WRITE_ACTION_PERMIT_user",
			matchedRule:   "/a/b/c",
		},
		{
			description:   "Group match",
			user:          "User2",
			path:          "/a/b/c",
			mode:          pathzpb.Mode_MODE_WRITE,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/b/c_MODE_WRITE_ACTION_PERMIT_group",
			matchedRule:   "/a/b/c",
		},
		{
			description:   "Prefix match",
			user:          "User1",
			path:          "/a/b/c/d[k1=v1]/e",
			mode:          pathzpb.Mode_MODE_WRITE,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/b/c_MODE_WRITE_ACTION_PERMIT_user",
			matchedRule:   "/a/b/c",
		},
		{
			description:   "Root match",
			user:          "User3",
			path:          "/a/b/c/d[k1=v1]/e",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/_MODE_READ_ACTION_PERMIT_user",
			matchedRule:   "/",
		},
		{
			description: "Root request",
			user:        "User1",
			path:        "/",
			mode:        pathzpb.Mode_MODE_READ,
			error:       false,
			result:      pathzpb.Action_ACTION_UNSPECIFIED,
		},
		{
			description:   "Wildcard Key match",
			user:          "User2",
			path:          "/a/d[k1=v2]/e/f",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_DENY,
			matchedRuleId: "/a/d[k1=*]/e/f_MODE_READ_ACTION_DENY_group",
			matchedRule:   "/a/d[k1=*]/e/f",
		},
		{
			description:   "User match",
			user:          "User1",
			path:          "/a/d[k1=v2]/e/f",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/d[k1=*]/e/f_MODE_READ_ACTION_PERMIT_user",
			matchedRule:   "/a/d[k1=*]/e/f",
		},
		{
			description:   "Exact Key match",
			user:          "User2",
			prefix:        "/a",
			path:          "/d[k1=v1]/e/f",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/d[k1=v1]/e_MODE_READ_ACTION_PERMIT_group",
			matchedRule:   "/a/d[k1=v1]/e",
		},
		{
			description:   "Deny overwrites permit",
			user:          "User1",
			prefix:        "/a",
			path:          "/d[k1=v1]/e/f",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_DENY,
			matchedRuleId: "/a/d[k1=v1]/e_MODE_READ_ACTION_DENY_user",
			matchedRule:   "/a/d[k1=v1]/e",
		},
		{
			description:   "Exact key match on first key",
			user:          "User1",
			prefix:        "/a",
			path:          "/d[k1=v1]/e/g[k2=v2]/h",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_DENY,
			matchedRuleId: "/a/d[k1=v1]/e/g[k2=*]/h_MODE_READ_ACTION_DENY_group",
			matchedRule:   "/a/d[k1=v1]/e/g[k2=*]/h",
		},
		{
			description:   "Deny overwrites permit in group",
			user:          "User1",
			prefix:        "/a",
			path:          "/d[k1=v2]/e/g[k2=v2]/h",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_DENY,
			matchedRuleId: "/a/d[k1=*]/e/g[k2=v2]/h_MODE_READ_ACTION_DENY_group",
			matchedRule:   "/a/d[k1=*]/e/g[k2=v2]/h",
		},
		{
			description:   "Multiple key match",
			user:          "User1",
			prefix:        "/a",
			path:          "/i[k3=v3][k4=v4]/j[k5=v5]/k/l",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_DENY,
			matchedRuleId: "/a/i[k3=v3][k4=*]/j[k5=*]/k_MODE_READ_ACTION_DENY_user",
			matchedRule:   "/a/i[k3=v3][k4=*]/j[k5=*]/k",
		},
		{
			description:   "Multiple key match wildcard",
			user:          "User1",
			prefix:        "/a",
			path:          "/i[k3=v4][k4=v4]/j[k5=v5]/k/l",
			mode:          pathzpb.Mode_MODE_READ,
			error:         false,
			result:        pathzpb.Action_ACTION_PERMIT,
			matchedRuleId: "/a/i[k3=*][k4=v4]/j[k5=v5]/k/l_MODE_READ_ACTION_PERMIT_user",
			matchedRule:   "/a/i[k3=*][k4=v4]/j[k5=v5]/k/l",
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			p, err := getGnmiPath(tc.path)
			if err != nil {
				t.Errorf("Error in getGnmiPath: %v", err)
			}
			var r *Result
			if tc.prefix != "" {
				var prefix *gnmipb.Path
				prefix, err = getGnmiPath(tc.prefix)
				if err != nil {
					t.Errorf("Error in getGnmiPath: %v", err)
				}
				r, err = processor.AuthorizeWithPrefix(tc.user, prefix, p, tc.mode)
			} else {
				r, err = processor.Authorize(tc.user, p, tc.mode)
			}
			if tc.error != (err != nil) {
				t.Errorf("Returned unexpected error: %v", err)
			}
			if !tc.error {
				if tc.result != r.Action {
					t.Errorf("Expect %v, got %v", tc.result, r.Action)
				}
				if tc.matchedRuleId != r.RuleId {
					t.Errorf("Expect %v, got %v", tc.matchedRuleId, r.RuleId)
				}
				if tc.matchedRule != r.MatchedRule {
					t.Errorf("Expect %v, got %v", tc.matchedRule, r.MatchedRule)
				}
			}
		})
	}
}

func TestGnsiPathzPolicyNil(t *testing.T) {

	t.Run("Authorize", func(t *testing.T) {
		var p *GnmiAuthzProcessor
		if _, err := p.Authorize("", nil, pathzpb.Mode_MODE_UNSPECIFIED); err == nil {
			t.Error("expected error in Authorize")
		}
	})

	t.Run("AuthorizeWithPrefix", func(t *testing.T) {
		var p *GnmiAuthzProcessor
		if _, err := p.AuthorizeWithPrefix("", nil, nil, pathzpb.Mode_MODE_UNSPECIFIED); err == nil {
			t.Error("expected error in AuthorizeWithPrefix")
		}
	})

	t.Run("UpdatePolicyFromFile", func(t *testing.T) {
		var p *GnmiAuthzProcessor
		if err := p.UpdatePolicyFromFile(""); err == nil {
			t.Error("expected error in UpdatePolicyFromFile")
		}
	})

	t.Run("UpdatePolicyFromProto", func(t *testing.T) {
		var p *GnmiAuthzProcessor
		if err := p.UpdatePolicyFromProto(nil); err == nil {
			t.Error("expected error in UpdatePolicyFromProto")
		}
	})

	t.Run("GetPolicy", func(t *testing.T) {
		var p *GnmiAuthzProcessor
		if err := p.GetPolicy(); err != nil {
			t.Error("expected nil policy")
		}
	})

	t.Run("insertPath", func(t *testing.T) {
		var n *gnmiAuthzNode
		if err := n.insertPath(nil, 0, nil, 0); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("authorize", func(t *testing.T) {
		var n *gnmiAuthzNode
		if res := n.authorize("", nil, pathzpb.Mode_MODE_UNSPECIFIED, 0, nil, 0, nil); !reflect.DeepEqual(res, Result{Action: pathzpb.Action_ACTION_UNSPECIFIED}) {
			t.Errorf("expected unspecified; got: %#v", res)
		}
	})

	t.Run("updatePermission", func(t *testing.T) {
		var p *permission
		if err := p.updatePermission(pathzpb.Action_ACTION_UNSPECIFIED, pathzpb.Mode_MODE_UNSPECIFIED, ""); err == nil {
			t.Error("expected error")
		}
	})

}
