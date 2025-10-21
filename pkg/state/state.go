package state

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
)

const (
	FULLUPDATE_WAITTIME        = 60 * time.Second
	MAX_DELETION_EVENTS        = 100
	HEART_BEAT_INTERVAL_MINUTE = 10
)

func Track(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover,
	fullRefreshMinutes int64,
	resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx)

	log.Info("Start periodic updates channel.", "durationMinutes", fullRefreshMinutes)

	fullUpdatesCh := make(chan string, 10)
	fullUpdatesCh <- "pi-start" // trigger full updates when pi agent starts
	go fullUpdateProcessor(fullUpdatesCh, ctx, ociClient, sd, resolveLatestTag)

	// random delay avoid all same Pi updates at the same time
	go startIntervalSync(ctx, fullRefreshMinutes, fullUpdatesCh)
	go startKeepAlive(ctx, sd)

	for {
		eventCh, errCh, err := ociClient.Subscribe(ctx)
		metrics.ContainerdSubscribeTotal.WithLabelValues("success").Add(1)
		if err != nil {
			metrics.ContainerdSubscribeTotal.WithLabelValues("fail").Add(1)
			log.Error(err, "Error when subscribe events from containerd, restart tracker.")
		} else {
			log.Info("Subscribed from containerd")

		SubscribeLoop:
			for {
				select {
				case <-ctx.Done():
					return nil

				case event, ok := <-eventCh:
					if !ok {
						log.Info("eventCh closed, restart the subscriber")
						break SubscribeLoop
					}
					log.Info("received image event", "image", event.Image.String(), "type", event.Type)
					metrics.ContainerdSubscribeEventTotal.WithLabelValues(string(event.Type)).Add(1)

					// Delete event will trigger full upates...
					if event.Type == oci.DeleteEvent {
						fullUpdatesCh <- "deleteEvent"
						continue
					}

					if _, err := update(ctx, ociClient, sd, event, false, resolveLatestTag); err != nil {
						log.Error(err, "received error when updating image")
						continue
					}
				case err, ok := <-errCh:
					if !ok {
						log.Info("errCh closed, restart the subscriber")
						break SubscribeLoop
					}
					log.Error(err, "event channel error, restart the subscriber.")
					break SubscribeLoop
				}
			} // subscribe for
		}

		log.Info("the subscriber need to be restarted, but I'll wait 3 seconds...")

		select {
		case <-time.After(time.Duration(3) * time.Second):
			log.Info("Now restart subscribe containerd")
		case <-ctx.Done():
			log.Info("context canceled, terminate tracker")
			return nil
		}

	}
}

// if full updates triggered (less than) MAX_DELETION_EVENTS in FULLUPDATE_WAITTIME
// the full update will only be called once.
func fullUpdateProcessor(events <-chan string, ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, resolveLatestTag bool) {
	var buffer []string
	log := logr.FromContextOrDiscard(ctx)
	timer := time.NewTimer(FULLUPDATE_WAITTIME)
	timer.Stop()

	flush := func() {
		if len(buffer) > 0 {
			all(ctx, ociClient, sd, resolveLatestTag)
			buffer = nil
			timer.Stop()
		}
	}

	for {
		select {
		case e := <-events:
			buffer = append(buffer, e)
			if len(buffer) == 1 {
				timer.Reset(FULLUPDATE_WAITTIME)
			}
			if len(buffer) >= MAX_DELETION_EVENTS {
				log.Info("Full updated triggered due to have 10 events", "lenBuffer", len(buffer))
				flush()
			}
		case <-timer.C:
			log.Info("Full updated triggered due to wait time passed since last event", "lenBuffer", len(buffer), "waitTime", FULLUPDATE_WAITTIME, "buffer", buffer)
			flush()
		}
	}
}

func all(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx).V(4)
	imgs, err := ociClient.ListImages(ctx)
	log.Info("Exeucte a full updates, list images: ", "imgs", imgs)
	if err != nil {
		log.Error(err, "ociClient.ListImages returns error")
		return err
	}

	metrics.AdvertisedKeys.Reset()
	metrics.AdvertisedImages.Reset()
	metrics.AdvertisedImageTags.Reset()
	metrics.AdvertisedImageDigests.Reset()
	errs := []error{}
	targets := map[string]interface{}{}
	keys := map[string]string{}
	for _, img := range imgs {
		_, skipDigests := targets[img.Digest.String()]

		if !(!resolveLatestTag && img.IsLatestTag()) {
			if tagName, ok := img.TagName(); ok {
				keys[tagName] = img.Registry
				metrics.AdvertisedImageDigests.WithLabelValues(img.Registry).Add(1)
			}
		}

		if !skipDigests {
			dgsts, err := oci.WalkImage(ctx, ociClient, img)
			if err != nil {
				errs = append(errs, err)
			}
			for _, d := range dgsts {
				keys[d] = img.Registry
			}
		}
		targets[img.Digest.String()] = img.Registry
		metrics.AdvertisedImages.WithLabelValues(img.Registry).Add(1)
	}
	keyList := []string{}
	for key, reg := range keys {
		keyList = append(keyList, key)
		metrics.AdvertisedKeys.WithLabelValues(reg).Add(1)
	}
	log.Info("Sync all images", "totalKeys", len(keyList))
	err = sd.Sync(ctx, keyList)
	if err != nil {
		return err
	}
	return errors.Join(errs...)
}

func update(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, event oci.ImageEvent, skipDigests, resolveLatestTag bool) (int, error) {
	keys := []string{}
	if !(!resolveLatestTag && event.Image.IsLatestTag()) {
		if tagName, ok := event.Image.TagName(); ok {
			keys = append(keys, tagName)
		}
	}
	if event.Type == oci.DeleteEvent {
		log := logr.FromContextOrDiscard(ctx)
		log.Error(errors.New("Shouldn't reach there"), "DeleteEvent should be handled by all()")
		return 0, nil
	}
	if !skipDigests {
		dgsts, err := oci.WalkImage(ctx, ociClient, event.Image)
		if err != nil {
			return 0, fmt.Errorf("could not get digests for image %s: %w", event.Image.String(), err)
		}
		keys = append(keys, dgsts...)
	}
	err := sd.Advertise(ctx, keys)
	if err != nil {
		return 0, fmt.Errorf("could not advertise image %s: %w", event.Image.String(), err)
	}
	if event.Type == oci.CreateEvent {
		// We don't know how many unique digest keys will be associated with the new image;
		// that can only be updated by the full image list sync in all().
		metrics.AdvertisedImages.WithLabelValues(event.Image.Registry).Add(1)
		if event.Image.Tag == "" {
			metrics.AdvertisedImageDigests.WithLabelValues(event.Image.Registry).Add(1)
		} else {
			metrics.AdvertisedImageTags.WithLabelValues(event.Image.Registry).Add(1)
		}
	}
	return len(keys), nil
}

func startIntervalSync(ctx context.Context, intervalMinutes int64, fullUpdatesCh chan<- string) {
	log := logr.FromContextOrDiscard(ctx)
	rand.Seed(time.Now().UnixNano())
	resetInMinutes := rand.Int63n(intervalMinutes)
	log.Info("fullUpdatesTimer will be reset in minutes", "minutes", resetInMinutes)

	select {
	case <-time.After(time.Duration(resetInMinutes) * time.Minute):
		log.Info("Interval update first trigger wait period over.")
	case <-ctx.Done():
		return
	}

	log.Info("Interval update first trigger full sync, then trigger for every", "minutes", intervalMinutes)
	fullUpdatesCh <- "ticker"

	// update for const interval
	expirationTicker := time.NewTicker(time.Duration(intervalMinutes) * time.Minute)
	defer expirationTicker.Stop()

	for {
		select {
		case <-expirationTicker.C:
			log.Info("By Ticker: Running scheduled image state update")
			fullUpdatesCh <- "ticker"
		case <-ctx.Done():
			return
		}
	}
}

func startKeepAlive(ctx context.Context, sd sd.ServiceDiscover) {
	log := logr.FromContextOrDiscard(ctx)
	rand.Seed(time.Now().UnixNano())
	resetInMinutes := rand.Int63n(HEART_BEAT_INTERVAL_MINUTE)
	log.Info("Heart beat timer will reset in", "minutes", resetInMinutes)

	select {
	case <-time.After(time.Duration(resetInMinutes) * time.Minute):
		log.Info("Heart beat first trigger wait period over.")
	case <-ctx.Done():
		return
	}

	log.Info("First heart beat starts, then trigger for every", "minutes", HEART_BEAT_INTERVAL_MINUTE)
	if err := sd.DoKeepAlive(ctx); err != nil {
		log.Error(err, "Error when do keepalive")
	}

	// update for const interval
	keepaliveTicker := time.NewTicker(time.Duration(HEART_BEAT_INTERVAL_MINUTE) * time.Minute)
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-keepaliveTicker.C:
			log.Info("By Ticker: Running scheduled image state update")
			if err := sd.DoKeepAlive(ctx); err != nil {
				log.Error(err, "Error when do keepalive")
			}
		case <-ctx.Done():
			return
		}
	}
}
