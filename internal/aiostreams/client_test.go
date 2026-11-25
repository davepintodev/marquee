package aiostreams_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/config"
)

func TestSearchDecodesFormatTagsAndSeasonPack(t *testing.T) {
	fake := aiostreamsfake.New()
	defer fake.Close()
	fake.Answer("series", "tt1:1", []byte(`{"success":true,"data":{"results":[
	{"filename":"Show.S01.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10+.mkv","size":9000000000,"cached":true,
	 "parsedFile":{"resolution":"2160p","quality":"WEB-DL","encode":"HEVC","seasons":[1],
	  "audioTags":["DD+","Atmos"],"audioChannels":["5.1"],"visualTags":["DV","HDR10+"],"seasonPack":true}},
	{"filename":"Show.S01E02.mkv","size":900000000,"cached":true,"parsedFile":{"resolution":"1080p"}}
	]}}`))
	c, err := aiostreams.New(aiostreams.Options{Manifest: config.Secret(fake.ManifestURL())})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Search(context.Background(), "series", "tt1:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 streams, got %d", len(got))
	}
	pf := got[0].ParsedFile
	if !reflect.DeepEqual(pf.AudioTags, []string{"DD+", "Atmos"}) ||
		!reflect.DeepEqual(pf.AudioChannels, []string{"5.1"}) ||
		!reflect.DeepEqual(pf.VisualTags, []string{"DV", "HDR10+"}) || !pf.SeasonPack {
		t.Errorf("format fields not decoded: %+v", pf)
	}
	if p := got[1].ParsedFile; p.AudioTags != nil || p.VisualTags != nil || p.SeasonPack {
		t.Errorf("a release without the fields keeps them empty: %+v", p)
	}
}
