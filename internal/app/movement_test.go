package app

import "testing"

func TestHorizontalMotionsStopAtTheEdgesUnlessWrapping(t *testing.T) {
	setupVisualTable(t)
	oldWrap := wrapColumns
	t.Cleanup(func() { wrapColumns = oldWrap })

	wrapColumns = false
	press(t, "h")
	if _, col := bufferTable.GetSelection(); col != 0 {
		t.Errorf("h in the first column must stay there, got column %d", col)
	}
	press(t, "b")
	if _, col := bufferTable.GetSelection(); col != 0 {
		t.Errorf("b in the first column must stay there, got column %d", col)
	}
	press(t, "$ l")
	if _, col := bufferTable.GetSelection(); col != 3 {
		t.Errorf("l in the last column must stay there, got column %d", col)
	}
	press(t, "5 w")
	if _, col := bufferTable.GetSelection(); col != 3 {
		t.Errorf("a count past the last column must stop there, got column %d", col)
	}
	press(t, "0 9 h")
	if _, col := bufferTable.GetSelection(); col != 0 {
		t.Errorf("a count past the first column must stop there, got column %d", col)
	}

	wrapColumns = true
	press(t, "h")
	if _, col := bufferTable.GetSelection(); col != 3 {
		t.Errorf("with wrap_columns h in the first column must reach the last, got column %d", col)
	}
	press(t, "l")
	if _, col := bufferTable.GetSelection(); col != 0 {
		t.Errorf("with wrap_columns l in the last column must reach the first, got column %d", col)
	}
}
