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

package database

import (
	"reflect"
	"testing"
)

func TestSMSFromNumber_BeforeSave(t *testing.T) {
	t.Parallel()

	db, _ := testDatabaseInstance.NewDatabase(t, nil)

	t.Run("label", func(t *testing.T) {
		t.Parallel()

		var n SMSFromNumber
		n.Label = ""
		_ = n.BeforeSave(db.RawDB())

		errs := n.ErrorsFor("label")
		if len(errs) < 1 {
			t.Fatal("expected error")
		}
	})

	t.Run("value", func(t *testing.T) {
		t.Parallel()

		var n SMSFromNumber
		n.Value = ""
		_ = n.BeforeSave(db.RawDB())

		errs := n.ErrorsFor("value")
		if len(errs) < 1 {
			t.Fatal("expected error")
		}
	})
}

func TestSMSFromNumbers(t *testing.T) {
	t.Parallel()

	db, _ := testDatabaseInstance.NewDatabase(t, nil)

	if err := db.CreateOrUpdateSMSFromNumbers([]*SMSFromNumber{
		{
			Label: "zzz",
			Value: "222-222-2222",
		},
		{
			Label: "aaa",
			Value: "111-111-1111",
		},
	}); err != nil {
		t.Fatal(err)
	}

	smsFromNumbers, err := db.SMSFromNumbers()
	if err != nil {
		t.Fatal(err)
	}

	var labels []string
	var values []string
	for _, v := range smsFromNumbers {
		labels = append(labels, v.Label)
		values = append(values, v.Value)
	}

	if got, want := labels, []string{"aaa", "zzz"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v to be %v", got, want)
	}
	if got, want := values, []string{"111-111-1111", "222-222-2222"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v to be %v", got, want)
	}
}