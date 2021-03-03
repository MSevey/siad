package renter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// TestReadResponseSet is a unit test for the readResponseSet.
func TestReadResponseSet(t *testing.T) {
	// Get a set and fill it up completely.
	n := 10
	c := make(chan *jobReadRegistryResponse)
	set := newReadResponseSet(c, n)
	go func() {
		for i := 0; i < n; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
	}()
	if set.responsesLeft() != n {
		t.Fatal("wrong number of responses left", set.responsesLeft(), n)
	}

	// Calling Next should work until it's empty.
	i := 0
	for set.responsesLeft() > 0 {
		resp := set.next(context.Background())
		if resp == nil {
			t.Fatal("resp shouldn't be nil")
		}
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
		i++
	}

	// Call Next one more time and close the context while doing so.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := set.next(ctx)
	if resp != nil {
		t.Fatal("resp should be nil")
	}

	// Collect all values.
	resps := set.collect(context.Background())
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Create another set that is collected right away.
	c = make(chan *jobReadRegistryResponse)
	set = newReadResponseSet(c, n)
	go func() {
		for i := 0; i < n; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
	}()
	resps = set.collect(context.Background())
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Create another set that is collected halfway and then cancelled.
	c = make(chan *jobReadRegistryResponse)
	set = newReadResponseSet(c, n/2)
	ctx, cancel = context.WithCancel(context.Background())
	go func(cancel context.CancelFunc) {
		for i := 0; i < n/2; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
		cancel()
	}(cancel)
	resps = set.collect(ctx)
	if len(resps) != n/2 {
		t.Fatal("wrong number of resps", len(resps), n/2)
	}
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Collect a set without responses with a closed context.
	set = newReadResponseSet(c, n)
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	resps = set.collect(ctx)
	if len(resps) != 0 {
		t.Fatal("resps should be empty", resps)
	}
}

// TestReadRegistryPruning makes sure the read registry stats object is pruned
// correctly.
func TestReadRegistryPruning(t *testing.T) {
	rrs := newReadRegistryStats(time.Second)

	// Add 2 times the max timings.
	toAdd := make([]float64, 2*registryStatsMaxTimings)
	rrs.managedAddTimings(toAdd)

	// The length should be the max.
	if rrs.timings.Len() != registryStatsMaxTimings {
		t.Fatal("wrong length", rrs.timings.Len(), registryStatsMaxTimings)
	}

	// Wait for the min age.
	time.Sleep(registryTimingMinAge)

	// Add 1 more timing to trigger the pruning.
	rrs.managedAddTimings([]float64{0})

	// The length should be registryStatsMinTimings.
	if rrs.timings.Len() != int(registryStatsMinTimings) {
		t.Fatal("wrong length", rrs.timings.Len(), registryStatsMinTimings)
	}
}

// TestReadRegistryStats is a unit test for the readRegistryStats.
func TestReadRegistryStats(t *testing.T) {
	// Test vars.
	initialEstimate := time.Second
	startTime := time.Now()

	// Declare a helper to create response sets from responses.
	testResponseSet := func(startTime time.Time, resps ...*jobReadRegistryResponse) *readRegistryStats {
		responseChan := make(chan *jobReadRegistryResponse, len(resps))
		for _, resp := range resps {
			responseChan <- resp
		}
		rrs := newReadRegistryStats(initialEstimate)
		rrs.threadedAddResponseSet(context.Background(), startTime, newReadResponseSet(responseChan, len(resps)))
		return rrs
	}

	// Declare tests.
	tests := []struct {
		resps  []*jobReadRegistryResponse
		result time.Duration
	}{
		// No responses.
		{
			resps:  nil,
			result: initialEstimate,
		},
		// Successful response without value.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticSignedRegistryValue: nil,
					staticCompleteTime:        startTime.Add(time.Second * 5),
				},
			},
			result: time.Second * 3,
		},
		// Response with error.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticErr:          errors.New("error"),
					staticCompleteTime: startTime.Add(time.Second * 5),
				},
			},
			result: initialEstimate,
		},
		// Single successful response.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{},
					staticErr:                 nil,
					staticCompleteTime:        startTime.Add(time.Second * 5),
				},
			},
			result: time.Second * 3,
		},
		// Mixed responses - single valid.
		{
			resps: []*jobReadRegistryResponse{
				// No response but success.
				{
					staticSignedRegistryValue: nil,
					staticCompleteTime:        startTime.Add(time.Second * 5),
				},
				// Error response.
				{
					staticErr:          errors.New("error"),
					staticCompleteTime: startTime.Add(time.Second * 2),
				},
				// Success.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 1,
						},
					},
					staticErr:          nil,
					staticCompleteTime: startTime.Add(time.Second * 10),
				},
			},
			result: time.Millisecond * 7500,
		},
		// Mixed responses - faster result.
		{
			resps: []*jobReadRegistryResponse{
				// Success.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 1,
						},
					},
					staticErr:          nil,
					staticCompleteTime: startTime.Add(time.Second * 5),
				},
				// Success with higher revision but slower.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 2,
						},
					},
					staticErr:          nil,
					staticCompleteTime: startTime.Add(time.Second * 6),
				},
			},
			result: time.Millisecond * 5500,
		},
	}

	// Run tests.
	for i, test := range tests {
		// Test a response set with 1 response that took 5 seconds.
		rrs := testResponseSet(startTime, test.resps...)
		if rrs.Estimate() != test.result {
			t.Fatalf("%v: results don't match %v != %v", i, rrs.Estimate(), test.result)
		}
	}
}
