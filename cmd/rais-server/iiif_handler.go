package main

import (
	"encoding/json"
	"fmt"
	"github.com/uoregon-libraries/rais-image-server/iiif"
	"image/jpeg"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func acceptsLD(req *http.Request) bool {
	for _, h := range req.Header["Accept"] {
		for _, accept := range strings.Split(h, ",") {
			if accept == "application/ld+json" {
				return true
			}
		}
	}

	return false
}

type IIIFHandler struct {
	Base          *url.URL
	BaseRegex     *regexp.Regexp
	BaseOnlyRegex *regexp.Regexp
	FeatureSet    *iiif.FeatureSet
	InfoPathRegex *regexp.Regexp
	TilePath      string
}

func NewIIIFHandler(u *url.URL, widths []int, tp string) *IIIFHandler {
	// The base feature set is level 1, then we add our extra features, tile sizes, etc
	fs := iiif.FeatureSet1()
	fs.RotationBy90s = true
	fs.TileSizes = make([]iiif.TileSize, 0)
	sf := []int{1, 2, 4, 8, 16, 32, 64}
	for _, val := range widths {
		fs.TileSizes = append(fs.TileSizes, iiif.TileSize{Width: val, ScaleFactors: sf})
	}

	rprefix := fmt.Sprintf(`^%s`, u.Path)
	return &IIIFHandler{
		Base:          u,
		BaseRegex:     regexp.MustCompile(rprefix + `/([^/]+)`),
		BaseOnlyRegex: regexp.MustCompile(rprefix + `/[^/]+$`),
		InfoPathRegex: regexp.MustCompile(rprefix + `/([^/]+)/info.json$`),
		TilePath:      tp,
		FeatureSet:    fs,
	}
}

func (ih *IIIFHandler) Route(w http.ResponseWriter, req *http.Request) {
	// Pull identifier from base so we know if we're even dealing with a valid
	// file in the first place
	p := req.RequestURI
	parts := ih.BaseRegex.FindStringSubmatch(p)

	// If it didn't even match the base, something weird happened, so we just
	// spit out a generic 404
	if parts == nil {
		http.NotFound(w, req)
		return
	}

	identifier := iiif.ID(parts[1])
	filepath := ih.TilePath + "/" + identifier.Path()

	res, err := NewImageResource(identifier, filepath)

	if err != nil {
		switch err {
		case ErrImageDoesNotExist:
			http.Error(w, "Image resource does not exist", 404)
		default:
			http.Error(w, err.Error(), 500)
		}
		return
	}

	defer res.Image.CleanupResources()

	// Check for base path and redirect if that's all we have
	if ih.BaseOnlyRegex.MatchString(p) {
		http.Redirect(w, req, p+"/info.json", 303)
		return
	}

	// Check for info path, and dispatch if it matches
	if ih.InfoPathRegex.MatchString(p) {
		ih.Info(w, req, res)
		return
	}

	// No info path should mean a full command path
	if u := iiif.NewURL(p); u.Valid() {
		ih.Command(w, req, u, res)
		return
	}

	// This means the URI was probably a command, but had an invalid syntax
	http.Error(w, "Invalid IIIF request", 400)
}

func (ih *IIIFHandler) Info(w http.ResponseWriter, req *http.Request, res *ImageResource) {
	rect, err := res.Image.GetDimensions()
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read image dimensions for %#v", res.ID), 500)
		return
	}

	info := ih.FeatureSet.Info()
	info.Width = rect.Dx()
	info.Height = rect.Dy()

	// The info id is actually the full URL to the resource, not just its ID
	info.ID = ih.Base.String() + "/" + res.ID.String()

	json, err := json.Marshal(info)
	if err != nil {
		log.Printf("ERROR!  Unable to marshal IIIFInfo response: %s", err)
		http.Error(w, "Server error", 500)
		return
	}

	// Set headers - content type is dependent on client
	ct := "application/json"
	if acceptsLD(req) {
		ct = "application/ld+json"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(json)
}

// Handles image processing operations.  Putting resize into the IIIFImage
// interface is necessary due to the way openjpeg operates on images - we must
// know which layer to decode to get the nearest valid image size when
// doing any resize operations.
func (ih *IIIFHandler) Command(w http.ResponseWriter, req *http.Request, u *iiif.URL, res *ImageResource) {
	// Send last modified time
	if err := sendHeaders(w, req, res.FilePath); err != nil {
		return
	}

	// Do we support this request?  If not, return a 501
	if !ih.FeatureSet.Supported(u) {
		http.Error(w, "Feature not supported", 501)
		return
	}

	img, err := res.Apply(u)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Encode as JPEG straight to the client
	if err = jpeg.Encode(w, img, &jpeg.Options{Quality: 80}); err != nil {
		http.Error(w, "Unable to encode jpeg", 500)
		log.Println("Unable to encode JPEG:", err)
		return
	}
}