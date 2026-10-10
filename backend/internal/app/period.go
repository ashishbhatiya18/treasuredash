package app

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Quarter naming follows the Indian fiscal year: the year runs April–March and
// is named by the year it *ends* in. FY 2026-27 (Apr 2026 – Mar 2027) is
// "FY27", so Jul–Sep 2026 is "Q2FY27" and Jan–Mar 2027 is "Q4FY27".
// Internally Quarter.FY holds the starting calendar year.

const DateFmt = "2006-01-02"

type Quarter struct {
	FY int // starting calendar year, e.g. 2026
	Q  int // 1..4
}

func (q Quarter) ID() string { return fmt.Sprintf("Q%dFY%02d", q.Q, (q.FY+1)%100) }

// FYLabel names the financial year in full, e.g. "FY2026-27".
func (q Quarter) FYLabel() string { return fmt.Sprintf("FY%d-%02d", q.FY, (q.FY+1)%100) }

func (q Quarter) Start() time.Time {
	month := time.Month(4 + (q.Q-1)*3)
	year := q.FY
	if q.Q == 4 {
		month, year = time.January, q.FY+1
	}
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}

func (q Quarter) End() time.Time { return q.Start().AddDate(0, 3, -1) }

// DefaultDue is the last day of the quarter's second month.
func (q Quarter) DefaultDue() time.Time { return q.Start().AddDate(0, 2, -1) }

func (q Quarter) Next() Quarter {
	if q.Q == 4 {
		return Quarter{q.FY + 1, 1}
	}
	return Quarter{q.FY, q.Q + 1}
}

func (q Quarter) Prev() Quarter {
	if q.Q == 1 {
		return Quarter{q.FY - 1, 4}
	}
	return Quarter{q.FY, q.Q - 1}
}

func QuarterOf(t time.Time) Quarter {
	m := int(t.Month())
	if m >= 4 {
		return Quarter{t.Year(), (m-4)/3 + 1}
	}
	return Quarter{t.Year() - 1, 4}
}

var quarterRe = regexp.MustCompile(`^Q([1-4])FY(\d{2})$`)

func ParseQuarter(id string) (Quarter, error) {
	m := quarterRe.FindStringSubmatch(id)
	if m == nil {
		return Quarter{}, fmt.Errorf("invalid quarter id %q (want e.g. Q3FY27)", id)
	}
	q, _ := strconv.Atoi(m[1])
	yy, _ := strconv.Atoi(m[2])
	return Quarter{2000 + yy - 1, q}, nil // FY27 starts in April 2026
}

func Today() time.Time {
	ist := time.FixedZone("IST", 5*3600+1800)
	n := time.Now().In(ist)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func ParseDate(s string) (time.Time, error) { return time.Parse(DateFmt, s) }
