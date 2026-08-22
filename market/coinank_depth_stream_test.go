package market

import "testing"

func TestCoinAnkDepthBookReconcilesFullAndIncrementalUpdates(t *testing.T) {
	book := newCoinAnkDepthBook()
	if _, err := book.apply(false, [][]string{{"100", "1"}}, nil); err == nil {
		t.Fatal("incremental before full snapshot was accepted")
	}
	depth, err := book.apply(true,
		[][]string{{"100", "1"}, {"99", "2"}},
		[][]string{{"101", "1"}, {"102", "2"}},
	)
	if err != nil {
		t.Fatalf("full apply: %v", err)
	}
	if depth.Bids[0][0] != "100" || depth.Asks[0][0] != "101" {
		t.Fatalf("full depth sorting = %#v/%#v", depth.Bids, depth.Asks)
	}
	depth, err = book.apply(false,
		[][]string{{"100", "0"}, {"98", "3"}},
		[][]string{{"101", "4"}, {"103", "1"}},
	)
	if err != nil {
		t.Fatalf("incremental apply: %v", err)
	}
	if len(depth.Bids) != 2 || depth.Bids[0][0] != "99" || depth.Bids[1][0] != "98" {
		t.Fatalf("incremental bids = %#v", depth.Bids)
	}
	if len(depth.Asks) != 3 || depth.Asks[0][0] != "101" || depth.Asks[0][1] != "4" {
		t.Fatalf("incremental asks = %#v", depth.Asks)
	}
}
