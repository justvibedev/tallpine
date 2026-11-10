package procmem

import "errors"

// ErrProcessGone means the process no longer exists or is a zombie.
var ErrProcessGone = errors.New("procmem: process has exited")

// ErrLineageUnsupported means this platform cannot walk one process's parents.
var ErrLineageUnsupported = errors.New("procmem: process lineage unsupported on this platform")

// maxChainLength stops a walk of a corrupt or cyclic parent chain.
const maxChainLength = 256

// parentLink is one process's place in the tree.
type parentLink struct {
	ppid    int
	session int
}

// ParentChain returns pid and its ancestors, nearest first, down to but not
// including pid 1. It costs one kernel read per ancestor; Snapshot shells out
// for the whole table, too slow to run per request. An ancestor that vanishes
// mid-walk ends the chain. pid itself must exist.
func ParentChain(pid int) ([]int, error) {
	chain := make([]int, 0, 8)
	for current := pid; current > 1 && len(chain) < maxChainLength; {
		link, err := readParentLink(current)
		if err != nil {
			if current == pid {
				return nil, err
			}
			break
		}
		chain = append(chain, current)
		if link.ppid == current {
			break
		}
		current = link.ppid
	}
	return chain, nil
}

// SessionOf returns pid's POSIX session id.
func SessionOf(pid int) (int, error) {
	link, err := readParentLink(pid)
	if err != nil {
		return 0, err
	}
	return link.session, nil
}
