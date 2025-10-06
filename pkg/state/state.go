package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/internal/channel"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
)

func Track(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, fullRefreshMinutes int64, resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx)
	eventCh, errCh, err := ociClient.Subscribe(ctx)
	if err != nil {
		return err
	}
	immediateCh := make(chan time.Time, 1)
	immediateCh <- time.Now()
	close(immediateCh)
	expirationTicker := time.NewTicker(time.Duration(fullRefreshMinutes) * time.Minute)
	defer expirationTicker.Stop()
	tickerCh := channel.Merge(immediateCh, expirationTicker.C)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tickerCh:
			log.Info("running scheduled image state update")
			if err := all(ctx, ociClient, sd, resolveLatestTag); err != nil {
				log.Error(err, "received errors when updating all images")
				continue
			}
		case event, ok := <-eventCh:
			if !ok {
				return errors.New("image event channel closed")
			}
			log.Info("received image event", "image", event.Image.String(), "type", event.Type)

			// Delete event will trigger full upates...
			if event.Type == oci.DeleteEvent {
				if err := all(ctx, ociClient, sd, resolveLatestTag); err != nil {
					log.Error(err, "received errors when updating all images")
				}
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

func all(ctx context.Context, ociClient oci.Client, sd sd.ServiceDiscover, resolveLatestTag bool) error {
	log := logr.FromContextOrDiscard(ctx).V(4)
	imgs, err := ociClient.ListImages(ctx)
	log.Info("list images: ", "imgs", imgs)
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
	for _, img := range imgs {
		_, skipDigests := targets[img.Digest.String()]
		// Handle the list re-sync as update events; this will also prevent the
		// update function from setting metrics values.
		event := oci.ImageEvent{Image: img, Type: oci.UpdateEvent}
		log.Info("sync image event", "image", event.Image.String(), "type", event.Type)
		keyTotal, err := update(ctx, ociClient, sd, event, skipDigests, resolveLatestTag)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		targets[img.Digest.String()] = nil
		metrics.AdvertisedKeys.WithLabelValues(img.Registry).Add(float64(keyTotal))
		metrics.AdvertisedImages.WithLabelValues(img.Registry).Add(1)
		if img.Tag == "" {
			metrics.AdvertisedImageDigests.WithLabelValues(event.Image.Registry).Add(1)
		} else {
			metrics.AdvertisedImageTags.WithLabelValues(event.Image.Registry).Add(1)
		}
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
