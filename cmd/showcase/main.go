// showcase — the mock media server behind Alexandria's store screenshots
// and marketing material. One invented catalog, served three ways:
//
//	Audiobookshelf dialect on :13378   (phone + TV audiobooks)
//	Jellyfin dialect       on :8096    (phone music + video, TV video)
//	Plex dialect           on :32400   (TV video + music, phone music)
//
// Point the apps at this machine's LAN address (the phone rejects
// localhost). Any username/password signs in. Nothing here is anyone's
// copyrighted work: titles, people and blurbs are invented, artwork is
// generated, audio is synthesized, video comes from tools/showcase/
// makeclips.swift (also synthetic).
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/showcase"
)

func main() {
	absAddr := flag.String("abs", ":13378", "listen address for the Audiobookshelf dialect")
	jfAddr := flag.String("jellyfin", ":8096", "listen address for the Jellyfin dialect")
	plexAddr := flag.String("plex", ":32400", "listen address for the Plex dialect")
	clips := flag.String("clips", defaultClipsDir(), "directory of rendered clips (tools/showcase/makeclips.swift)")
	flag.Parse()

	ids := showcase.ClipIDs(*clips)
	cat := showcase.Build(time.Now(), ids)
	// Plex and Jellyfin each get half the films, shows and artists, so a
	// Home that merges both servers shows two libraries, not one twice.
	absSrv := showcase.New(cat, *clips)
	plexSrv := showcase.New(cat.Half(0), *clips)
	jfSrv := showcase.New(cat.Half(1), *clips)
	log.Printf("showcase: %d books, %d albums/%d tracks, %d films, %d shows/%d episodes, %d clips (%s)",
		len(cat.Books), len(cat.Albums), len(cat.Tracks), len(cat.Movies), len(cat.Shows), len(cat.Episodes), len(ids), *clips)
	if len(ids) == 0 {
		log.Printf("showcase: no clips rendered yet — video plays nothing; run: swiftc -O tools/showcase/makeclips.swift -o /tmp/makeclips && /tmp/makeclips %s <specs>", *clips)
	}
	for _, ip := range lanIPs() {
		log.Printf("showcase: LAN address %s  →  ABS http://%s%s  Jellyfin http://%s%s  Plex http://%s%s", ip, ip, *absAddr, ip, *jfAddr, ip, *plexAddr)
	}
	serve := func(name, addr string, h http.Handler) {
		s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		log.Printf("showcase: %s listening on %s", name, addr)
		if err := s.ListenAndServe(); err != nil {
			log.Fatalf("%s: %v", name, err)
		}
	}
	go serve("abs", *absAddr, absSrv.ABSHandler())
	go serve("jellyfin", *jfAddr, jfSrv.JellyfinHandler())
	serve("plex", *plexAddr, plexSrv.PlexHandler())
}

func defaultClipsDir() string {
	if d := os.Getenv("SHOWCASE_CLIPS"); d != "" {
		return d
	}
	return filepath.Join("showcase-clips")
}

func lanIPs() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
				out = append(out, fmt.Sprint(ipn.IP))
			}
		}
	}
	return out
}
