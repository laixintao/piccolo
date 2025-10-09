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
	FULLUPDATE_WAITTIME = 60 * time.Second
	MAX_DELETION_EVENTS = 100
	JITTER_PERCENT      = 0.1
)

func jitterDuration(base time.Duration) time.Duration {
	factor := 1 + (rand.Float64()*2-1)*JITTER_PERCENT
	return time.Duration(float64(base) * factor)
}

func Track(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, fullRefreshMinutes int64, resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx)
	eventCh, errCh, err := ociClient.Subscribe(ctx)
	if err != nil {
		return err
	}

	jitterUpdateDuration := jitterDuration(time.Duration(fullRefreshMinutes) * time.Minute)
	log.Info("Start periodic updates channel.", "durationMinutes", jitterUpdateDuration.Minutes())

	expirationTicker := time.NewTicker(jitterUpdateDuration)
	defer expirationTicker.Stop()

	fullUpdatesCh := make(chan string, 10)
	fullUpdatesCh <- "pi-start" // trigger full updates when pi agent starts
	go fullUpdateProcessor(fullUpdatesCh, ctx, ociClient, sd, resolveLatestTag)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-expirationTicker.C:
			log.Info("By Ticker: Running scheduled image state update")
			fullUpdatesCh <- "ticker"

		case event, ok := <-eventCh:
			if !ok {
				return errors.New("image event channel closed")
			}
			log.Info("received image event", "image", event.Image.String(), "type", event.Type)

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
				return errors.New("image error channel closed")
			}
			log.Error(err, "event channel error")
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
			log.Info("Full updated triggered due to 10 seoncds passed since last event", "lenBuffer", len(buffer))
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
	var keyList []string
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
