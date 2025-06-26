package factory_reset

import (
	"context"
	"encoding/json"
	"fmt"

	syspb "github.com/openconfig/gnoi/factory_reset"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
)

func StartFactoryReset(sc syspb.FactoryResetClient, ctx context.Context) {
	fmt.Println("Start Factory Reset.")
	ctx = utils.SetUserCreds(ctx)
	if *config.Args == "" {
		panic("--jsonin must be set.")
	}

	req := &syspb.StartRequest{}
	if err := json.Unmarshal([]byte(*config.Args), req); err != nil {
		fmt.Println("Unable to parse JSON input: ", err)
		panic("Unable to parse JSON input.")
	}

	resp, err := sc.Start(ctx, req)
	if err != nil {
		panic(err.Error())
	}

	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
