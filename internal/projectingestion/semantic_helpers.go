package projectingestion

import "fmt"

func dedupeReferences(references []Reference) []Reference {
	seen := make(map[string]struct{}, len(references))
	out := make([]Reference, 0, len(references))
	for _, ref := range references {
		key := ref.Kind + "\x00" + ref.TargetName + "\x00" + ref.EnclosingSymbolName + "\x00" + fmt.Sprint(ref.StartLine) + "\x00" + fmt.Sprint(ref.StartByte)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func dedupeCalls(calls []Call) []Call {
	seen := make(map[string]struct{}, len(calls))
	out := make([]Call, 0, len(calls))
	for _, call := range calls {
		key := call.CallerName + "\x00" + call.CalleeName + "\x00" + call.Receiver + "\x00" + fmt.Sprint(call.StartLine) + "\x00" + fmt.Sprint(call.StartByte)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	return out
}
