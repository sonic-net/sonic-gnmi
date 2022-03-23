package transl_utils

import (
	"bytes"
	"encoding/json"
	"strings"
	"fmt"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-telemetry/common_utils"
	"context"
	"log/syslog"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"

)

var (
    Writer *syslog.Writer
)

func __log_audit_msg(ctx context.Context, reqType string, uriPath string, err error) {
    var err1 error
    username := "invalid"
    statusMsg := "failure"
    errMsg := "None"
    if (err == nil) {
        statusMsg = "success"
    } else {
        errMsg = err.Error()
    }

    if Writer == nil {
        Writer, err1 = syslog.Dial("", "", (syslog.LOG_LOCAL4), "")
        if (err1 != nil) {
            log.V(2).Infof("Could not open connection to syslog with error =%v", err1.Error())
            return
        }
    }

    common_utils.GetUsername(ctx, &username)

    auditMsg := fmt.Sprintf("User \"%s\" request \"%s %s\" status - %s error - %s",
                            username, reqType, uriPath, statusMsg, errMsg)
    Writer.Info(auditMsg)
}

func GnmiTranslFullPath(prefix, path *gnmipb.Path) *gnmipb.Path {

	fullPath := &gnmipb.Path{Origin: path.Origin}
	if path.GetElement() != nil {
		fullPath.Element = append(prefix.GetElement(), path.GetElement()...)
	}
	if path.GetElem() != nil {
		fullPath.Elem = append(prefix.GetElem(), path.GetElem()...)
	}
	return fullPath
}

/* Populate the URI path corresponding GNMI paths. */
func PopulateClientPaths(prefix *gnmipb.Path, paths []*gnmipb.Path, path2URI *map[*gnmipb.Path]string) error {
	var req string

	/* Fetch the URI for each GET URI. */
	for _, path := range paths {
		ConvertToURI(prefix, path, &req)
		(*path2URI)[path] = req
	}

	return nil
}

/* Populate the URI path corresponding each GNMI paths. */
func ConvertToURI(prefix *gnmipb.Path, path *gnmipb.Path, req *string) error {
	fullPath := path
	if prefix != nil {
		fullPath = GnmiTranslFullPath(prefix, path)
	}

	elems := fullPath.GetElem()
	*req = "/"

	if elems != nil {
		/* Iterate through elements. */
		for i, elem := range elems {
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			*req += elem.GetName()
			key := elem.GetKey()
			/* If no keys are present end the element with "/" */
			if key == nil {
				*req += "/"
			}

			/* If keys are present , process the keys. */
			if key != nil {
				for k, v := range key {
					log.V(6).Infof("elem : %#v %#v", k, v)
					*req += "[" + k + "=" + v + "]"
				}

				/* Append "/" after all keys are processed. */
				*req += "/"
			}
		}
	}

	/* Trim the "/" at the end which is not required. */
	*req = strings.TrimSuffix(*req, "/")
	return nil
}

/* Fill the values from TransLib. */
func TranslProcessGet(uriPath string, op *string, ctx context.Context) (*gnmipb.TypedValue, error) {
	var jv []byte
	var data []byte
	rc, _ := common_utils.GetContext(ctx)

	req := translib.GetRequest{Path:uriPath, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("GET operation failed with error =%v", err.Error())
			return nil, err
		}
		req.ClientVersion = nver
	}
	if rc.Auth.AuthEnabled {
		req.AuthEnabled = true
	}
	resp, err1 := translib.Get(req)

	if isTranslibSuccess(err1) {
		data = resp.Payload
	} else {
		log.V(2).Infof("GET operation failed with error =%v, %v", resp.ErrSrc, err1.Error())
		return nil, err1
	}

	dst := new(bytes.Buffer)
	json.Compact(dst, data)
	jv = dst.Bytes()


	/* Fill the values into GNMI data structures . */
	return &gnmipb.TypedValue{
		Value: &gnmipb.TypedValue_JsonIetfVal{
		JsonIetfVal: jv,
		}}, nil

}

/* Delete request handling. */
func TranslProcessDelete(uri string, ctx context.Context) error {
	var str3 string
	payload := []byte(str3)
	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path:uri, Payload:payload, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("DELETE operation failed with error =%v", err.Error())
			return err
		}
		req.ClientVersion = nver
	}
	if rc.Auth.AuthEnabled {
		req.AuthEnabled = true
	}
	resp, err := translib.Delete(req)
	if err != nil{
		log.V(2).Infof("DELETE operation failed with error =%v, %v", resp.ErrSrc, err.Error())
		return err
	}

	return nil
}

/* Replace request handling. */
func TranslProcessReplace(uri string, t *gnmipb.TypedValue, ctx context.Context) error {
	/* Form the CURL request and send to client . */
	str := string(t.GetJsonIetfVal())
	str3 := strings.Replace(str, "\n", "", -1)
	log.V(2).Info("Incoming JSON body is", str)

	payload := []byte(str3)
	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path:uri, Payload:payload, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("REPLACE operation failed with error =%v", err.Error())
			return err
		}
		req.ClientVersion = nver
	}
	if rc.Auth.AuthEnabled {
		req.AuthEnabled = true
	}
	resp, err1 := translib.Replace(req)

	if err1 != nil{
		log.V(2).Infof("REPLACE operation failed with error =%v, %v", resp.ErrSrc, err1.Error())
		return err1
	}


	return nil
}

/* Update request handling. */
func TranslProcessUpdate(uri string, t *gnmipb.TypedValue, ctx context.Context) error {
	/* Form the CURL request and send to client . */
	str := string(t.GetJsonIetfVal())
	str3 := strings.Replace(str, "\n", "", -1)
	log.V(2).Info("Incoming JSON body is", str)

	payload := []byte(str3)
	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path:uri, Payload:payload, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("UPDATE operation failed with error =%v", err.Error())
			return err
		}
		req.ClientVersion = nver
	}
	if rc.Auth.AuthEnabled {
		req.AuthEnabled = true
	}
	resp, err := translib.Update(req)
	if err != nil{
		switch err.(type) {
		case tlerr.NotFoundError:
			//If Update fails, it may be due to object not existing in this case use Replace to create and update the object.
			resp, err = translib.Replace(req)
		default:
			log.V(2).Infof("UPDATE operation failed with error =%v, %v", resp.ErrSrc, err.Error())
			return err
		}
	}
	if err != nil{
		log.V(2).Infof("UPDATE operation failed with error =%v, %v", resp.ErrSrc, err.Error())
		return err
	}
	return nil
}

func TranslProcessBulk(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update, prefix *gnmipb.Path, ctx context.Context) error {
	var br translib.BulkRequest
	var uri string

        var deleteUri []string
        var replaceUri []string
        var updateUri []string

	rc, ctx := common_utils.GetContext(ctx)
	log.V(2).Info("TranslProcessBulk Called")
	var nver translib.Version
	var err error
	if rc.BundleVersion != nil {
		nver, err = translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("Bundle Version Check failed with error =%v", err.Error())
			return err
		}
	}
	for _,d := range delete {
		ConvertToURI(prefix, d, &uri)
		var str3 string
		payload := []byte(str3)
		req := translib.SetRequest{
			Path: uri,
			Payload: payload,
			User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles},
		}
		if rc.BundleVersion != nil {
			req.ClientVersion = nver
		}
		if rc.Auth.AuthEnabled {
			req.AuthEnabled = true
		}
		br.DeleteRequest = append(br.DeleteRequest, req)
                deleteUri = append(deleteUri, uri)
	}
	for _,r := range replace {
		ConvertToURI(prefix, r.GetPath(), &uri)
		str := string(r.GetVal().GetJsonIetfVal())
		str3 := strings.Replace(str, "\n", "", -1)
		log.V(2).Info("Incoming JSON body is", str)
		payload := []byte(str3)
		req := translib.SetRequest{
			Path: uri,
			Payload: payload,
			User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles},
		}
		if rc.BundleVersion != nil {
			req.ClientVersion = nver
		}
		if rc.Auth.AuthEnabled {
			req.AuthEnabled = true
		}
		br.ReplaceRequest = append(br.ReplaceRequest, req)
                replaceUri = append(replaceUri, uri)
	}
	for _,u := range update {
		ConvertToURI(prefix, u.GetPath(), &uri)
		str := string(u.GetVal().GetJsonIetfVal())
		str3 := strings.Replace(str, "\n", "", -1)
		log.V(2).Info("Incoming JSON body is", str)
		payload := []byte(str3)
		req := translib.SetRequest{
			Path: uri,
			Payload: payload,
			User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles},
		}
		if rc.BundleVersion != nil {
			req.ClientVersion = nver
		}
		if rc.Auth.AuthEnabled {
			req.AuthEnabled = true
		}
		br.UpdateRequest = append(br.UpdateRequest, req)
                updateUri = append(updateUri, uri)
	}

	resp,err := translib.Bulk(br)

        i := 0
	for _,d := range resp.DeleteResponse {
            __log_audit_msg(ctx, "DELETE", deleteUri[i], d.Err)
            i++
        }
        i = 0
	for _,r := range resp.ReplaceResponse {
            __log_audit_msg(ctx, "REPLACE", replaceUri[i], r.Err)
            i++
        }
        i = 0
	for _,u := range resp.UpdateResponse {
            __log_audit_msg(ctx, "UPDATE", updateUri[i], u.Err)
            i++
        }

	var errors []string
	if err != nil{
		log.V(2).Info("BULK SET operation failed with error(s):")
		for _,d := range resp.DeleteResponse {
			if d.Err != nil {
				log.V(2).Infof("%s=%v", d.Err.Error(), d.ErrSrc)
				errors = append(errors, d.Err.Error())
			}
		}
		for _,r := range resp.ReplaceResponse {
			if r.Err != nil {
				log.V(2).Infof("%s=%v", r.Err.Error(), r.ErrSrc)
				errors = append(errors, r.Err.Error())
			}
		}
		for _,u := range resp.UpdateResponse {
			if u.Err != nil {
				log.V(2).Infof("%s=%v", u.Err.Error(), u.ErrSrc)
				errors = append(errors, u.Err.Error())
			}
		}
		return fmt.Errorf("SET failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

/* Action/rpc request handling. */
func TranslProcessAction(uri string, payload []byte, ctx context.Context) ([]byte, error) {
	rc, ctx := common_utils.GetContext(ctx)
	req := translib.ActionRequest{User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("Action operation failed with error =%v", err.Error())
			return nil, err
		}
		req.ClientVersion = nver
	}
	if rc.Auth.AuthEnabled {
		req.AuthEnabled = true
	}
	req.Path = uri
	req.Payload = payload

	resp, err := translib.Action(req)
        __log_audit_msg(ctx, "ACTION", uri, err)

	if err != nil{
		log.V(2).Infof("Action operation failed with error =%v, %v", resp.ErrSrc, err.Error())
		return nil, err
	}
	return resp.Payload, nil
}

/* Fetch the supported models. */
func GetModels() []gnmipb.ModelData {

	gnmiModels := make([]gnmipb.ModelData, 0, 1)
	supportedModels, _ := translib.GetModels()
	for _,model := range supportedModels {
		gnmiModels = append(gnmiModels, gnmipb.ModelData{
			Name: model.Name,
			Organization: model.Org,
			Version: model.Ver,

		})
	}
	return gnmiModels
}

func isTranslibSuccess(err error) bool {
        if err != nil && err.Error() != "Success" {
                return false
        }

        return true
}
