/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package types

import (
	"strings"

	"github.com/gogo/protobuf/proto"
	"github.com/google/go-cmp/cmp"
)

// protoKnownFieldsEqual returns true if the provided proto messages are equal,
// ignoring any unknown fields found during unmarshal (ie, this ignores XXX_*
// fields).
// This is not a substitute for [proto.Equal] and should not be used as a full
// equility check.
// Do not use this method lightly or without a strong reason to do so.
func protoKnownFieldsEqual(a, b proto.Message) bool {
	return cmp.Equal(a, b, cmp.FilterPath(func(path cmp.Path) bool {
		if field, ok := path.Last().(cmp.StructField); ok {
			return strings.HasPrefix(field.Name(), "XXX_")
		}
		return false
	}, cmp.Ignore()))
}
