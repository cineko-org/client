package cgv

import "testing"

func TestCinemaControlsWaitForSPARender(t *testing.T) {
	root, err := NewAdapter(t.Context(), localBrowserTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.page.SetContent(`<script>
	window.chosen = '';
	setTimeout(() => {
	 const favorite = document.createElement('button');
	 favorite.textContent = '자주가는 CGV 목록 수정';
	 favorite.onclick = () => {
	  favorite.remove();
	  setTimeout(() => {
	   const region = document.createElement('button');
	   region.textContent = '서울(10)';
	   region.onclick = () => setTimeout(() => {
	    const theater = document.createElement('button');
	    theater.textContent = '용산아이파크몰';
	    theater.onclick = () => { window.chosen = '용산아이파크몰'; };
	    document.body.append(theater);
	   }, 250);
	   document.body.append(region);
	  }, 250);
	 };
	 document.body.append(favorite);
	}, 250);
	</script>`); err != nil {
		t.Fatal(err)
	}
	if err := root.selectCinemaControls("서울", "용산아이파크몰"); err != nil {
		t.Fatal(err)
	}
	value, err := root.page.Evaluate("window.chosen")
	if err != nil || value != "용산아이파크몰" {
		t.Fatalf("chosen = %v, %v", value, err)
	}
}
