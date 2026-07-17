package usecases

import (
	"log"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

func requestRemoteFeatureData(
	entity spineapi.EntityRemoteInterface,
	remoteFeature func(spineapi.EntityRemoteInterface) spineapi.FeatureRemoteInterface,
	localFeature func() spineapi.FeatureLocalInterface,
	function model.FunctionType,
	debug bool,
	label string,
) {
	remote := remoteFeature(entity)
	local := localFeature()
	if remote == nil || local == nil {
		return
	}
	operation := remote.Operations()[function]
	if operation == nil || !operation.Read() {
		return
	}
	if _, err := local.RequestRemoteData(function, nil, nil, remote); err != nil && debug {
		log.Printf("[%s] requesting %s failed: %s", label, function, err.String())
	}
}
