package transl_utils

import (
	"bytes"
	"encoding/json"
	"strings"
	"fmt"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/Azure/sonic-telemetry/translib"
)

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
func TranslProcessGet(uriPath string, op *string) (*gnmipb.TypedValue, error) {
	var jv []byte
	var data []byte

	req := translib.GetRequest{Path:uriPath}
	resp, err1 := translib.Get(req)

	if isTranslibSuccess(err1) {
		data = resp.Payload
	} else {
		log.V(2).Infof("GET operation failed with error =%v", resp.ErrSrc)
		return nil, fmt.Errorf("GET failed for this message")
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
func TranslProcessDelete(uri string) error {
	var str3 string
	payload := []byte(str3)
	req := translib.SetRequest{Path:uri, Payload:payload}
	resp, err := translib.Delete(req)
	if err != nil{
		log.V(2).Infof("DELETE operation failed with error =%v", resp.ErrSrc)
		return fmt.Errorf("DELETE failed for this message")
	}

	return nil
}

/* Replace request handling. */
func TranslProcessReplace(uri string, t *gnmipb.TypedValue) error {
	/* Form the CURL request and send to client . */
	str := string(t.GetJsonIetfVal())
	str3 := strings.Replace(str, "\n", "", -1)
	log.V(2).Infof("Incoming JSON body is", str)

	payload := []byte(str3)
	req := translib.SetRequest{Path:uri, Payload:payload}
	resp, err1 := translib.Create(req)
	if err1 != nil{
		//If Create fails, it may be due to object already existing/can not be created
		// such as interface, in this case use Update.
		resp, err1 = translib.Update(req)
	}
	if err1 != nil{
		log.V(2).Infof("REPLACE operation failed with error =%v", resp.ErrSrc)
		return fmt.Errorf("REPLACE failed for this message")
	}


	return nil
}

/* Update request handling. */
func TranslProcessUpdate(uri string, t *gnmipb.TypedValue) error {
	/* Form the CURL request and send to client . */
	str := string(t.GetJsonIetfVal())
	str3 := strings.Replace(str, "\n", "", -1)
	log.V(2).Infof("Incoming JSON body is", str)

	payload := []byte(str3)
	req := translib.SetRequest{Path:uri, Payload:payload}
	resp, err := translib.Create(req)
	if err != nil{
		//If Create fails, it may be due to object already existing/can not be created
		// such as interface, in this case use Update.
		resp, err = translib.Update(req)
	}
	if err != nil{
		log.V(2).Infof("UPDATE operation failed with error =%v", resp.ErrSrc)
		return fmt.Errorf("UPDATE failed for this message")
	}
	return nil
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
