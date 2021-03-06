// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package stats produces statistics.
package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/google/exposure-notifications-verification-server/internal/project"
	"github.com/google/exposure-notifications-verification-server/pkg/cache"
	"github.com/google/exposure-notifications-verification-server/pkg/controller"
	"github.com/google/exposure-notifications-verification-server/pkg/database"
	"github.com/google/exposure-notifications-verification-server/pkg/rbac"
	"github.com/google/exposure-notifications-verification-server/pkg/render"
)

// StatsType represents a type of stat.
type StatsType int64

const (
	_ StatsType = iota
	StatsTypeCSV
	StatsTypeJSON
)

// Controller is a stats controller.
type Controller struct {
	cacher cache.Cacher
	db     *database.Database
	h      render.Renderer
}

// New creates a new stats controller.
func New(ctx context.Context, cacher cache.Cacher, db *database.Database, h render.Renderer) *Controller {
	return &Controller{
		cacher: cacher,
		db:     db,
		h:      h,
	}
}

// authorizeFromContext attempts to pull authorization from the context. It
// returns false if authorization failed.
func authorizeFromContext(ctx context.Context) (*database.Realm, bool) {
	membership := controller.MembershipFromContext(ctx)
	if membership != nil && membership.Can(rbac.StatsRead) {
		return membership.Realm, true
	}

	realm := controller.RealmFromContext(ctx)
	if realm != nil {
		return realm, true
	}

	return nil, false
}

// csvFilename returns the formatted filename for now.
func csvFilename(name string) string {
	nowFormatted := time.Now().Format(project.RFC3339Squish)
	return fmt.Sprintf("%s-%s.csv", nowFormatted, name)
}
