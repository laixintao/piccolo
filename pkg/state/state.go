package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/internal/logging"
	"github.com/laixintao/piccolo/internal/randduration"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
)

const (
	FULLUPDATE_WAITTIME = 60 * time.Second
	HEART_BEAT_INTERVAL = 10 * time.Minute
	MAX_DELETION_EVENTS = 100
)

func Track(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover,
	fullRefreshMinutes int64,
	resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("component", logging.Piccolo, "subsystem", "state")
	ctx = logr.NewContext(ctx, log)

	log.Info("image tracking started", "event", "tracking_started", "refresh_minutes", fullRefreshMinutes)

	fullUpdatesCh := make(chan string, 10)
	go fullUpdateProcessor(fullUpdatesCh, ctx, ociClient, sd, resolveLatestTag)

	// random delay avoid all same Pi updates at the same time
	go startIntervalSync(ctx, fullRefreshMinutes, fullUpdatesCh)
	go startKeepAlive(ctx, sd)

	for {
		ociCtx, calcenOciClient := context.WithCancel(ctx)
		eventCh, errCh, cErrCh, err := ociClient.Subscribe(ociCtx)

		metrics.ContainerdSubscribeTotal.WithLabelValues("success").Add(1)
		if err != nil {
			metrics.ContainerdSubscribeTotal.WithLabelValues("fail").Add(1)
			log.Error(err, "containerd subscription failed", "event", "subscription_failed")
		} else {
			log.Info("containerd image subscription established", "event", "subscription_started")

		SubscribeLoop:
			for {
				select {
				case <-ctx.Done():
					return nil

				case event, ok := <-eventCh:
					if !ok {
						log.Info("containerd image event stream closed", "event", "subscription_closed", "stream", "images")
						break SubscribeLoop
					}
					eventCtx := logging.Context(ctx, log.WithValues("image", event.ImageName, "namespace", event.Namespace))
					eventLog := logr.FromContextOrDiscard(eventCtx)
					eventLog.V(4).Info("containerd image event received", "event", "image_event", "event_type", event.Type, "digest", event.Image.Digest.String())
					metrics.ContainerdSubscribeEventTotal.WithLabelValues(string(event.Type)).Add(1)

					// Delete event will trigger full upates...
					if event.Type == oci.DeleteEvent {
						fullUpdatesCh <- "deleteEvent"
						continue
					}

					if _, err := update(eventCtx, ociClient, sd, event, false, resolveLatestTag); err != nil {
						eventLog.Error(err, "image advertisement failed", "event", "image_update_failed")
						continue
					}
				// SDK error, can not found image, in this case, no need to
				// restart the subscribe
				case err, ok := <-errCh:
					if !ok {
						log.Info("containerd event stream closed", "event", "subscription_closed")
						break SubscribeLoop
					}
					log.Error(err, "containerd image event could not be read", "event", "image_event_failed")
					continue
				case err, ok := <-cErrCh:
					if !ok {
						log.Info("containerd event stream closed", "event", "subscription_closed")
						break SubscribeLoop
					}
					log.Error(err, "containerd event stream failed", "event", "subscription_failed")
					break SubscribeLoop
				} // select
			} // subscribe for
		}

		calcenOciClient()
		log.Info("retrying containerd subscription", "event", "subscription_retry", "delay", "3s")

		select {
		case <-time.After(time.Duration(3) * time.Second):
			log.V(4).Info("restarting containerd subscription", "event", "subscription_restarting")
		case <-ctx.Done():
			log.Info("image tracking stopped", "event", "tracking_stopped")
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
				log.V(4).Info("full sync triggered by event count", "event", "full_sync_triggered", "event_count", len(buffer))
				flush()
			}
		case <-timer.C:
			log.V(4).Info("full sync triggered after batching events", "event", "full_sync_triggered", "event_count", len(buffer), "wait", FULLUPDATE_WAITTIME.String(), "triggers", buffer)
			flush()
		}
	}
}

func all(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, resolveLatestTag bool) (retErr error) {
	ctx = logging.Context(ctx, logr.FromContextOrDiscard(ctx))
	log := logr.FromContextOrDiscard(ctx)
	start := time.Now()
	defer func() {
		if retErr != nil {
			log.Error(retErr, "full image sync failed", "event", "full_sync_finished", "result", "error", "latency", time.Since(start).String())
			return
		}
		log.Info("full image sync finished", "event", "full_sync_finished", "result", "ok", "latency", time.Since(start).String())
	}()
	imgs, err := ociClient.ListImages(ctx)
	log.V(4).Info("images collected for full sync", "event", "images_listed", "image_count", len(imgs))
	if err != nil {
		log.Error(err, "could not list images for full sync", "event", "image_list_failed")
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
				// Advertise the tag scoped by the architectures we actually
				// hold content for, so that a puller on a different
				// architecture is never routed to this node for the tag.
				arches, err := oci.ImageArchitectures(ctx, ociClient, img.Digest)
				if err != nil {
					log.Error(err, "could not determine image architectures", "event", "architecture_check_failed", "image", img.String())
				}
				for _, arch := range arches {
					keys[oci.ArchTagKey(tagName, arch)] = img.Registry
				}
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
	log.Info("image keys ready for full sync", "event", "full_sync_prepared", "image_count", len(imgs), "key_count", len(keyList))
	err = sd.Sync(ctx, keyList)
	if err != nil {
		return err
	}
	return errors.Join(errs...)
}

func update(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, event oci.ImageEvent, skipDigests, resolveLatestTag bool) (int, error) {
	log := logr.FromContextOrDiscard(ctx)
	keys := []string{}
	if !(!resolveLatestTag && event.Image.IsLatestTag()) {
		if tagName, ok := event.Image.TagName(); ok {
			keys = append(keys, tagName)
			arches, err := oci.ImageArchitectures(ctx, ociClient, event.Image.Digest)
			if err != nil {
				log.Error(err, "could not determine image architectures", "event", "architecture_check_failed", "digest", event.Image.Digest.String())
			}
			for _, arch := range arches {
				keys = append(keys, oci.ArchTagKey(tagName, arch))
			}
		}
	}
	if event.Type == oci.DeleteEvent {
		log.Error(errors.New("delete event requires full sync"), "unexpected delete event in incremental update", "event", "image_update_failed")
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
	interval := time.Duration(intervalMinutes) * time.Minute
	sleepDuration := randduration.RandomDuration(interval)
	log.V(4).Info("full sync scheduled", "event", "full_sync_scheduled", "delay", sleepDuration.String(), "interval", interval.String())

	select {
	case <-time.After(sleepDuration):
		log.V(4).Info("initial full sync delay elapsed", "event", "full_sync_due")
	case <-ctx.Done():
		return
	}

	log.V(4).Info("periodic full sync started", "event", "full_sync_timer_started", "interval", interval.String())
	fullUpdatesCh <- "ticker"

	// update for const interval
	expirationTicker := time.NewTicker(interval)
	defer expirationTicker.Stop()

	for {
		select {
		case <-expirationTicker.C:
			log.V(4).Info("periodic full sync due", "event", "full_sync_due")
			fullUpdatesCh <- "ticker"
		case <-ctx.Done():
			return
		}
	}
}

func startKeepAlive(ctx context.Context, sd sd.ServiceDiscover) {
	log := logr.FromContextOrDiscard(ctx)
	sleepDuration := randduration.RandomDuration(HEART_BEAT_INTERVAL)
	log.V(4).Info("keepalive scheduled", "event", "keepalive_scheduled", "delay", sleepDuration.String(), "interval", HEART_BEAT_INTERVAL.String())

	select {
	case <-time.After(sleepDuration):
		log.V(4).Info("initial keepalive delay elapsed", "event", "keepalive_due")
	case <-ctx.Done():
		return
	}

	log.V(4).Info("periodic keepalive started", "event", "keepalive_timer_started", "interval", HEART_BEAT_INTERVAL.String())
	if err := sd.DoKeepAlive(ctx); err != nil {
		log.Error(err, "keepalive failed", "event", "keepalive_failed")
	}

	// update for const interval
	keepaliveTicker := time.NewTicker(HEART_BEAT_INTERVAL)
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-keepaliveTicker.C:
			log.V(4).Info("periodic keepalive due", "event", "keepalive_due")
			if err := sd.DoKeepAlive(ctx); err != nil {
				log.Error(err, "keepalive failed", "event", "keepalive_failed")
			}
		case <-ctx.Done():
			return
		}
	}
}
