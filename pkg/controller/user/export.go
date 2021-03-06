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

package user

import (
	"encoding/csv"
	"net/http"

	"github.com/google/exposure-notifications-server/pkg/logging"
	"github.com/google/exposure-notifications-verification-server/pkg/controller"
	"github.com/google/exposure-notifications-verification-server/pkg/pagination"
	"github.com/google/exposure-notifications-verification-server/pkg/rbac"
)

func (c *Controller) HandleExport() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := logging.FromContext(ctx)

		membership := controller.MembershipFromContext(ctx)
		if membership == nil {
			controller.MissingMembership(w, r, c.h)
			return
		}
		if !membership.Can(rbac.UserRead) {
			controller.Unauthorized(w, r, c.h)
			return
		}

		currentRealm := membership.Realm

		pageParams := &pagination.PageParams{
			Page:  0,
			Limit: 10000,
		}
		memberships, _, err := currentRealm.ListMemberships(c.db, pageParams)
		if err != nil {
			controller.InternalError(w, r, c.h, err)
			return
		}

		w.Header().Add("Content-Disposition", "")
		w.Header().Add("Content-Type", "text/CSV")

		csvWriter := csv.NewWriter(w)
		for _, membership := range memberships {
			user := membership.User
			if err := csvWriter.Write([]string{user.Email, user.Name}); err != nil {
				logger.Errorw("error writing record to csv:", "error", err)
				break
			}
		}
		csvWriter.Flush()
	})
}
