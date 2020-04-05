package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	fluxapi "github.com/fluxcd/flux/pkg/api"
	fluxapi_v9 "github.com/fluxcd/flux/pkg/api/v9"
	fluxhttp "github.com/fluxcd/flux/pkg/http"
	fluxclient "github.com/fluxcd/flux/pkg/http/client"
	"github.com/fluxcd/flux/pkg/image"
)

type HookContext struct {
	key          []byte
	registryHost string
}

type HookHandler func(s fluxapi.Server, ctx HookContext, w http.ResponseWriter, r *http.Request)

var Sources = map[string]HookHandler{}

// -- used for all handlers

const timeout = 10 * time.Second

func log(msg ...interface{}) {
	fmt.Fprintln(os.Stderr, msg...)
}

// --

func HandlerFromEndpoint(baseDir, apiUrl string, ep Endpoint) (string, http.Handler, error) {
	// 1. find the relevant Source (e.g., DockerHub)
	sourceHandler, ok := Sources[ep.Source]
	if !ok {
		return "", nil, fmt.Errorf("unknown source %q, check sources.go for possible values", ep.Source)
	}

	// 2. load the key so it can be used in the handler, and get the
	// digest so it can be used to route to this handler
	key, err := ioutil.ReadFile(filepath.Join(baseDir, ep.KeyPath))
	if err != nil {
		return "", nil, fmt.Errorf("cannot load key from %q: %s", ep.KeyPath, err.Error())
	}

	sha := sha256.New()
	sha.Write(key)
	sha.Write([]byte(ep.RegistryHost))
	digest := fmt.Sprintf("%x", sha.Sum(nil))

	registryHost := strings.TrimRight(ep.RegistryHost, "/")

	apiClient := fluxclient.New(http.DefaultClient, fluxhttp.NewAPIRouter(), apiUrl, fluxclient.Token(""))

	// 3. construct a handler from the above
	return digest, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHandler(apiClient, HookContext{ key, registryHost}, w, r)
	}), nil
}

func doImageNotify(s fluxapi.Server, w http.ResponseWriter, r *http.Request, img string) {
	ref, err := image.ParseRef(img)
	if err != nil {
		http.Error(w, "Cannot parse image in webhook payload", http.StatusBadRequest)
		log("could not parse image from hook payload:", img, ":", err.Error())
		return
	}
	change := fluxapi_v9.Change{
		Kind: fluxapi_v9.ImageChange,
		Source: fluxapi_v9.ImageUpdate{
			Name: ref.Name,
		},
	}
	ctx := r.Context()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.NotifyChange(ctx, change)
	w.WriteHeader(http.StatusOK)
}
