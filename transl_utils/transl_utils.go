package transl_utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/syslog"
	"strings"

	"github.com/Azure/sonic-mgmt-common/translib"
	pathutil "github.com/Azure/sonic-mgmt-common/translib/path"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	Writer *syslog.Writer
)

var (
	transLibOpMap map[int]string
)

func init() {
	transLibOpMap = map[int]string{
		translib.REPLACE: "REPLACE",
		translib.UPDATE:  "UPDATE",
		translib.DELETE:  "DELETE",
	}
}

func __log_audit_msg(ctx context.Context, reqType string, uriPath string, err error) {
	var err1 error
	username := "invalid"
	statusMsg := "failure"
	errMsg := "None"
	if err == nil {
		statusMsg = "success"
	} else {
		errMsg = err.Error()
	}

	if Writer == nil {
		Writer, err1 = syslog.Dial("", "", (syslog.LOG_LOCAL4), "")
		if err1 != nil {
			log.V(2).Infof("Could not open connection to syslog with error =%v", err1.Error())
			return
		}
	}

	common_utils.GetUsername(ctx, &username)

	auditMsg := fmt.Sprintf("User \"%s\" request \"%s %s\" status - %s error - %s",
		username, reqType, uriPath, statusMsg, errMsg)
	Writer.Info(auditMsg)
}

// ToStatus returns a gRPC status object for a translib error.
func ToStatus(err error) *status.Status {
	if err == nil {
		return nil
	}

	log.V(3).Infof("Translib error type=%T; value=%v", err, err)
	code := codes.Unknown
	data := "Operation failed"
	var s *status.Status

	switch err := err.(type) {
	case tlerr.TranslibSyntaxValidationError:
		code = codes.InvalidArgument
		data = err.ErrorStr.Error()
	case tlerr.TranslibUnsupportedClientVersion, tlerr.InvalidArgsError, tlerr.NotSupportedError:
		code = codes.InvalidArgument
		data = err.Error()
	case tlerr.InternalError:
		code = codes.Internal
		data = err.Error()
	case tlerr.NotFoundError:
		code = codes.NotFound
		data = err.Error()
	case tlerr.AlreadyExistsError:
		code = codes.AlreadyExists
		data = err.Error()
	case tlerr.TranslibCVLFailure:
		code = codes.InvalidArgument
		data = err.CVLErrorInfo.ConstraintErrMsg
		if len(data) == 0 {
			data = "Validation failed"
		}
	case tlerr.TranslibTransactionFail:
		code = codes.Aborted
		data = "Transaction failed. Please try again"
	case tlerr.TranslibRedisClientEntryNotExist:
		code = codes.NotFound
		data = "Resource not found"
	case tlerr.AuthorizationError:
		code = codes.PermissionDenied
		data = err.Error()
	case interface{ GRPCStatus() *status.Status }:
		s = err.GRPCStatus()
	default:
		s = status.FromContextError(err)
	}

	if s == nil {
		s = status.New(code, data)
	}
	if log.V(3) {
		log.Infof("gRPC status code=%v; msg=%v", s.Code(), s.Message())
	}
	return s
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
func PopulateClientPaths(prefix *gnmipb.Path, paths []*gnmipb.Path, path2URI *map[*gnmipb.Path]string, addWildcardKeys bool) error {
	opts := []pathutil.PathValidatorOpt{
		&pathutil.AppendModulePrefix{},
	}
	if addWildcardKeys {
		opts = append(opts, &pathutil.AddWildcardKeys{})
	}
	for _, path := range paths {
		req, err := ConvertToURI(prefix, path, opts...)
		if err != nil {
			return err
		}
		(*path2URI)[path] = req
	}

	return nil
}

// ConvertToURI returns translib path for a gnmi Path
func ConvertToURI(prefix, path *gnmipb.Path, opts ...pathutil.PathValidatorOpt) (string, error) {
	fullPath := path
	if prefix != nil {
		fullPath = GnmiTranslFullPath(prefix, path)
	}

	if len(opts) == 0 {
		opts = append(opts, &pathutil.AppendModulePrefix{})
	}
	pv := pathutil.NewPathValidator(opts...)
	if err := pv.Validate(fullPath); err != nil {
		return "", err
	}

	return ygot.PathToString(fullPath)
}

/* GetTranslibFmtType is a helper that converts gnmi Encoding to supported format types in translib */
func getTranslFmtType(encoding gnmipb.Encoding) translib.TranslibFmtType {

	if encoding == gnmipb.Encoding_PROTO {
		return translib.TRANSLIB_FMT_YGOT
	}
	// default to ietf_json as translib supports either Ygot or ietf_json
	return translib.TRANSLIB_FMT_IETF_JSON

}

/* Fill the values from TransLib. */
func TranslProcessGet(uriPath string, op *string, ctx context.Context, encoding gnmipb.Encoding) (*gnmipb.TypedValue, *translib.GetResponse, error) {
	var jv []byte
	var data []byte
	rc, _ := common_utils.GetContext(ctx)
	qp := translib.QueryParameters{Content: "all"}
	fmtType := getTranslFmtType(encoding)
	req := translib.GetRequest{Path: uriPath, FmtType: fmtType, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}, QueryParams: qp}
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("GET operation failed with error =%v", err.Error())
			return nil, nil, err
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
		return nil, nil, err1
	}

	/* When Proto is requested we use ValueTree to generate scalar values in the data_client.*/
	if encoding == gnmipb.Encoding_PROTO {
		return nil, &resp, nil
	} else {
		dst := new(bytes.Buffer)
		json.Compact(dst, data)
		jv = dst.Bytes()

		/* Fill the values into GNMI data structures . */
		return &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
			}}, nil, nil
	}

}

/* Delete request handling. */
func TranslProcessDelete(prefix, delPath *gnmipb.Path, ctx context.Context) error {
	uri, err := ConvertToURI(prefix, delPath)
	if err != nil {
		return err
	}

	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path: uri, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
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
	_, err = translib.Delete(req)
	if err != nil {
		log.V(2).Infof("DELETE operation failed with error %v", err.Error())
		return err
	}

	return nil
}

/* Replace request handling. */
func TranslProcessReplace(prefix *gnmipb.Path, entry *gnmipb.Update, ctx context.Context) error {
	uri, err := ConvertToURI(prefix, entry.GetPath())
	if err != nil {
		return err
	}

	payload := entry.GetVal().GetJsonIetfVal()
	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path: uri, Payload: payload, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
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
	_, err1 := translib.Replace(req)

	if err1 != nil {
		log.V(2).Infof("REPLACE operation failed with error %v", err1.Error())
		return err1
	}

	return nil
}

/* Update request handling. */
func TranslProcessUpdate(prefix *gnmipb.Path, entry *gnmipb.Update, ctx context.Context) error {
	uri, err := ConvertToURI(prefix, entry.GetPath())
	if err != nil {
		return err
	}

	payload := entry.GetVal().GetJsonIetfVal()
	rc, _ := common_utils.GetContext(ctx)
	req := translib.SetRequest{Path: uri, Payload: payload, User: translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}}
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
	_, err = translib.Update(req)
	if err != nil {
		switch err.(type) {
		case tlerr.NotFoundError:
			//If Update fails, it may be due to object not existing in this case use Replace to create and update the object.
			_, err = translib.Replace(req)
		default:
			log.V(2).Infof("UPDATE operation failed with error %v", err.Error())
			return err
		}
	}
	if err != nil {
		log.V(2).Infof("UPDATE operation failed with error %v", err.Error())
		return err
	}
	return nil
}

// TranslProcessBulk - Process Bulk Set request
func TranslProcessBulk(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update, prefix *gnmipb.Path, ctx context.Context) error {

	var uri string
	var err error
	var payload []byte
	var resp translib.BulkResponse
	var errors []string
	rc, ctx := common_utils.GetContext(ctx)
	br := translib.BulkRequest{}

	//set ClientVersion
	if rc.BundleVersion != nil {
		nver, err := translib.NewVersion(*rc.BundleVersion)
		if err != nil {
			log.V(2).Infof("Bulk Set operation failed with error =%v", err.Error())
			return err
		}
		br.ClientVersion = nver
	}
	//set User roles
	br.User = translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles}

	//set Auth setting
	if rc.Auth.AuthEnabled {
		br.AuthEnabled = true
	}
	log.V(2).Info("TranslProcessBulk Called")
	for _, d := range delete {
		fullPath := GnmiTranslFullPath(prefix, d)
		if uri, err = ConvertToURI(nil, fullPath); err != nil {
			return err
		}

		bulkReqEntry := translib.BulkRequestEntry{}
		bulkReqEntry.Entry = translib.SetRequest{
			Path:    uri,
			Payload: nil}
		bulkReqEntry.Operation = translib.DELETE
		br.Request = append(br.Request, bulkReqEntry)
	}

	for _, r := range replace {
		uri, err = ConvertToURI(prefix, r.GetPath())
		if err != nil {
			return err
		}
		switch v := r.GetVal().GetValue().(type) {
		case *gnmipb.TypedValue_JsonIetfVal:
			payload = v.JsonIetfVal
		default:
			return status.Errorf(codes.InvalidArgument, "unsupported value type %T for path %s", v, uri)
		}
		log.V(5).Infof("Replace path = '%s', payload = %s", uri, payload)
		bulkReqEntry := translib.BulkRequestEntry{}
		bulkReqEntry.Entry = translib.SetRequest{
			Path:    uri,
			Payload: payload}
		bulkReqEntry.Operation = translib.REPLACE
		br.Request = append(br.Request, bulkReqEntry)
	}
	for _, u := range update {
		uri, err = ConvertToURI(prefix, u.GetPath())
		if err != nil {
			return err
		}
		switch v := u.GetVal().GetValue().(type) {
		case *gnmipb.TypedValue_JsonIetfVal:
			payload = v.JsonIetfVal
		default:
			return status.Errorf(codes.InvalidArgument, "unsupported value type %T for path %s", v, uri)
		}
		log.V(5).Infof("Update path = '%s', payload = %s", uri, payload)
		bulkReqEntry := translib.BulkRequestEntry{}
		bulkReqEntry.Entry = translib.SetRequest{
			Path:    uri,
			Payload: payload}
		bulkReqEntry.Operation = translib.UPDATE
		br.Request = append(br.Request, bulkReqEntry)
	}

	resp, err = translib.Bulk(br)

	for k := range resp.Response {
		__log_audit_msg(ctx, transLibOpMap[resp.Response[k].Operation], br.Request[k].Entry.Path, resp.Response[k].Entry.Err)
		if resp.Response[k].Entry.Err != nil {
			log.Warningf("%s=%v", resp.Response[k].Entry.Err.Error(), resp.Response[k].Entry.ErrSrc)
			errors = append(errors, resp.Response[k].Entry.Err.Error())
		}
	}

	if err != nil && len(errors) == 0 { //Global error
		log.Errorf("Bulk Operation failed with Error: %v", err.Error())
		errors = append(errors, err.Error())
	}

	if len(errors) > 0 {
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

	if err != nil {
		log.V(2).Infof("Action operation failed with error %v", err.Error())
		return nil, err
	}
	return resp.Payload, nil
}

/* Fetch the supported models. */
func GetModels() []gnmipb.ModelData {

	gnmiModels := make([]gnmipb.ModelData, 0, 1)
	supportedModels, _ := translib.GetModels()
	for _, model := range supportedModels {
		gnmiModels = append(gnmiModels, gnmipb.ModelData{
			Name:         model.Name,
			Organization: model.Org,
			Version:      model.Ver,
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
