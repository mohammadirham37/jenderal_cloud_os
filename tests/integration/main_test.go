// Package integration contains end-to-end API tests (see tests/testenv).
package integration

import (
	"testing"

	"github.com/itk-cloud/itk-cloud/tests/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type (
	Env    = testenv.Env
	Client = testenv.Client
	Resp   = testenv.Resp
)

type envOpts = testenv.Opts

func setup(t *testing.T, opts envOpts) *Env { return testenv.Setup(t, opts) }

func str(m map[string]any, k string) string { return testenv.Str(m, k) }
