//go:build !matcher_noprefetch

package matcher

// prefetchMinLive: below this many live orders the book is cache-resident
// and ApplyBatch is a plain loop. Build with -tags matcher_noprefetch to
// disable the look-ahead entirely (benchmark comparison).
const prefetchMinLive = 1 << 18

// lookaheadStages: how many of the three look-ahead stages run.
const lookaheadStages = 2
