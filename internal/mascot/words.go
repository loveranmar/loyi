package mascot

import "math/rand"

// Activity is what the app is doing right now. It drives both the mascot face
// and the rotating status word. Cognition (thinking, reading) uses wolf/dog
// verbs; concrete steps (running) stay literal so it's always clear what's
// actually happening.
type Activity int

const (
	ActReady Activity = iota
	ActThinking
	ActReading
	ActWriting
	ActRunning
	ActSuccess
	ActError
)

// Face returns the mascot face that goes with an activity.
func (a Activity) Face() State {
	switch a {
	case ActReady:
		return Idle
	case ActSuccess:
		return Success
	case ActError:
		return Error
	default:
		return Thinking
	}
}

// Working reports whether an activity should keep the status word rotating.
func (a Activity) Working() bool {
	switch a {
	case ActThinking, ActReading, ActWriting, ActRunning:
		return true
	default:
		return false
	}
}

// Words is the single table of status-word pools, easy to edit later.
var Words = map[Activity][]string{
	ActReady:    {"ready"},
	ActThinking: {"sniffing…", "tracking…", "on the scent…"}, // signature core loop
	ActReading:  {"sniffing the repo…", "following tracks…", "scouting…", "mapping the den…"},
	ActWriting:  {"building…", "shaping…", "patching…", "burying it…"},
	ActRunning:  {"running…", "fetching…", "off and running…"},
	ActSuccess:  {"got it", "done", "nailed it", "fetched", "caught it"},
	ActError:    {"lost the trail", "hit a wall", "that didn't land", "dropped it"},
}

// thinkingExtra mixes into the thinking rotation so it stays fresh without
// drowning out the signature trio.
var thinkingExtra = []string{
	"digging…", "fetching…", "chewing on it…", "pawing through…",
	"nosing around…", "hunting…", "rummaging…",
}

// Cycler produces status words for the current activity, rotating calmly. The
// thinking rotation keeps the signature trio front and center and mixes an
// extended word in every fourth step.
type Cycler struct {
	act  Activity
	step int
	core int
	rnd  *rand.Rand
}

func NewCycler(rnd *rand.Rand) *Cycler {
	return &Cycler{act: ActReady, rnd: rnd}
}

// Set switches to a new activity and returns its first word.
func (c *Cycler) Set(a Activity) string {
	c.act = a
	c.step = 0
	c.core = 0
	return c.word()
}

// Next advances the rotation and returns the next word.
func (c *Cycler) Next() string {
	c.step++
	return c.word()
}

func (c *Cycler) word() string {
	switch c.act {
	case ActThinking:
		if c.step > 0 && c.step%4 == 0 {
			return thinkingExtra[c.rnd.Intn(len(thinkingExtra))]
		}
		core := Words[ActThinking]
		w := core[c.core%len(core)]
		c.core++
		return w
	case ActSuccess, ActError:
		pool := Words[c.act]
		return pool[c.rnd.Intn(len(pool))]
	default:
		pool := Words[c.act]
		if len(pool) == 0 {
			return ""
		}
		return pool[c.step%len(pool)]
	}
}
