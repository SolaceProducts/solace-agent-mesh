// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type locatedVolumeList interface {
	ListLocation() string
	ListIsSingleHostFile() bool
}

func ReportVolumeList(volumes volume.Service, flags *features.Registry, logger *slog.Logger) {
	if flags == nil || !flags.IsEnabled(volumesFeatureKey) {
		return
	}
	list, ok := volumes.(locatedVolumeList)
	if !ok {
		return
	}
	logger.Info("volume list opened; every gateway and AWE of one deployment must name the same one", "volumeList", list.ListLocation())
	if !list.ListIsSingleHostFile() {
		return
	}
	logger.Warn("the volume list is a SQLite file under the volume root, which is safe only while every gateway and AWE process runs on one host; set SAM_VOLUME_DATABASE_URL or DATABASE_URL, or volume_service.database_url on the gateway and volume_database_url on the AWE, to a database all of them reach")
}
