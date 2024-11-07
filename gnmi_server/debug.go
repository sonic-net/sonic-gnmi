////////////////////////////////////////////////////////////////////////////////
//                                                                            //
//  Copyright 2023 Broadcom. The term Broadcom refers to Broadcom Inc. and/or //
//  its subsidiaries.                                                         //
//                                                                            //
//  Licensed under the Apache License, Version 2.0 (the "License");           //
//  you may not use this file except in compliance with the License.          //
//  You may obtain a copy of the License at                                   //
//                                                                            //
//     http://www.apache.org/licenses/LICENSE-2.0                             //
//                                                                            //
//  Unless required by applicable law or agreed to in writing, software       //
//  distributed under the License is distributed on an "AS IS" BASIS,         //
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  //
//  See the License for the specific language governing permissions and       //
//  limitations under the License.                                            //
//                                                                            //
////////////////////////////////////////////////////////////////////////////////

package gnmi

import (
	"sort"
	"time"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-mgmt-common/translib/path"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	spb_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"github.com/sonic-net/sonic-gnmi/transl_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (srv *Server) GetSubscribePreferences(req *spb_gnoi.SubscribePreferencesReq, stream spb_gnoi.Debug_GetSubscribePreferencesServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}

	translPaths := make([]translib.IsSubscribePath, 0, len(req.GetPath()))
	for i, p := range req.GetPath() {
		reqPath, err := transl_utils.ConvertToURI(nil, p,
			&path.AppendModulePrefix{}, &path.AddWildcardKeys{})
		if err != nil {
			return status.Error(codes.InvalidArgument, "Unknown path: "+path.String(p))
		}

		translPaths = append(translPaths, translib.IsSubscribePath{
			ID:   uint32(i),
			Path: reqPath,
			Mode: translib.TargetDefined,
		})
	}

	rc, _ := common_utils.GetContext(ctx)
	trResp, err := translib.IsSubscribeSupported(translib.IsSubscribeRequest{
		Paths: translPaths,
		User:  translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles},
	})
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	// When the path supports on_change but some of its subpaths do not, extra entries
	// gets appended with same request ID. Group such entries by ID.
	sort.Slice(trResp, func(i, j int) bool {
		return trResp[i].ID < trResp[j].ID ||
			(trResp[i].ID == trResp[j].ID && trResp[i].Path < trResp[j].Path)
	})

	for _, r := range trResp {
		pathStr, err := path.New(r.Path)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		pref := &spb_gnoi.SubscribePreference{
			Path:              pathStr,
			WildcardSupported: r.IsWildcardSupported,
			OnChangeSupported: r.IsOnChangeSupported,
			TargetDefinedMode: gnmipb.SubscriptionMode_SAMPLE,
			MinSampleInterval: uint64(r.MinInterval) * uint64(time.Second),
		}
		if !r.IsSubPath && hasOnChangeDisabledSubpath(r.ID, trResp) {
			pref.OnChangeSupported = false
		}
		if r.IsOnChangeSupported && r.PreferredType == translib.OnChange {
			pref.TargetDefinedMode = gnmipb.SubscriptionMode_ON_CHANGE
		}

		if err = stream.Send(pref); err != nil {
			return err
		}
	}

	return nil
}

func hasOnChangeDisabledSubpath(id uint32, allPrefs []*translib.IsSubscribeResponse) bool {
	for _, p := range allPrefs {
		if p.ID == id && p.IsSubPath && !p.IsOnChangeSupported {
			return true
		}
	}
	return false
}
