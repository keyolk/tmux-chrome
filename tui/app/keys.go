package app

import "github.com/keyolk/tuikit/keys"

const (
	ctxGlobal    keys.Ctx = "global"
	ctxList      keys.Ctx = "list"
	ctxGroups    keys.Ctx = "groups"
	ctxTabs      keys.Ctx = "tabs"
	ctxSearch    keys.Ctx = "search"
	ctxHalted    keys.Ctx = "halted"
	ctxConfirm   keys.Ctx = "confirm"
	ctxPrompt    keys.Ctx = "prompt"
	ctxPicker    keys.Ctx = "picker"
	ctxChecklist keys.Ctx = "checklist"
	ctxHelp      keys.Ctx = "help"
)

// stacks are every context stack the UI can be in, innermost first.
// Overlays own the keyboard: their stacks hold no outer context, so a letter
// typed into a prompt can never reach a list action.
var stacks = [][]keys.Ctx{
	{ctxGroups, ctxList, ctxGlobal},
	{ctxTabs, ctxList, ctxGlobal},
	{ctxHalted, ctxGroups, ctxList, ctxGlobal},
	{ctxHalted, ctxTabs, ctxList, ctxGlobal},
	{ctxSearch},
	{ctxConfirm},
	{ctxPrompt},
	{ctxPicker},
	{ctxChecklist},
	{ctxHelp},
}

// needsBridge are actions that talk to Chrome. While the bridge is down they stay bound (the
// key answers with a toast) but leave the footer, which then lists what
// still works.
var needsBridge = map[string]bool{
	"group.new": true, "group.rename": true, "group.color": true, "group.merge": true,
	"group.ungroup": true, "group.close": true, "group.open": true, "group.grab": true,
	"tab.move": true, "tab.ungroup": true, "tab.close": true, "tab.pin": true,
	"tab.reload": true, "tab.new": true, "tab.open": true, "tab.grab": true,
	"undo": true, "clean": true, "group.enter": true, "tab.focus": true, "search.open": true,
}

func keymap() keys.Keymap {
	b := func(action string, k []string, ctx keys.Ctx, group, help string, footer int) keys.Binding {
		return keys.Binding{Action: action, Keys: k, Ctx: ctx, Group: group, Help: help, Footer: footer}
	}
	upper := func(x keys.Binding) keys.Binding { x.LatinOnly = true; return x }
	short := func(x keys.Binding, s string) keys.Binding { x.Short = s; return x }
	move := func(x keys.Binding, grp string) keys.Binding { x.FooterGroup = grp; return x }
	section := func(x keys.Binding, s string) keys.Binding { x.Section = s; return x }

	return keys.New(
		keys.Binding{Action: "quit", Keys: []string{"q", "ctrl+c"}, Ctx: ctxGlobal, Group: "기본", Help: "종료", Pinned: true, Footer: 20},
		keys.Binding{Action: "help", Keys: []string{"?"}, Ctx: ctxGlobal, Group: "기본", Help: "도움말", Pinned: true, Footer: 19},
		b("search", []string{"/"}, ctxGlobal, "기본", "검색", 1),
		b("scope", []string{"tab"}, ctxGlobal, "기본", "검색 범위 바꾸기", 9),
		b("palette", []string{":"}, ctxGlobal, "기본", "명령 팔레트", 8),
		keys.Binding{Action: "refresh", Keys: []string{"ctrl+r"}, Ctx: ctxGlobal, Group: "기본", Help: "새로고침", Footer: 12,
			Labels: map[string]string{"down": "지금 재연결", "legacy": "extension reload"}},
		upper(b("undo", []string{"U"}, ctxGlobal, "기본", "닫은 탭 되살리기", 11)),
		upper(b("clean", []string{"C"}, ctxGlobal, "기본", "정리 (stale·그룹 없는 탭·고아 그룹)", 13)),

		section(move(b("down", []string{"j", "down"}, ctxList, "이동", "아래", 2), "이동"), "list.move"),
		section(move(b("up", []string{"k", "up"}, ctxList, "이동", "위", 2), "이동"), "list.move"),
		section(move(b("left", []string{"h", "left"}, ctxList, "이동", "그룹 패널로", 3), "패널"), "list.move"),
		section(move(b("right", []string{"l", "right"}, ctxList, "이동", "탭 패널로", 3), "패널"), "list.move"),
		section(keys.Binding{Action: "top", Keys: []string{"home"}, Ctx: ctxList, Group: "이동", Help: "맨 위", NoPalette: true}, "list.move"),
		section(keys.Binding{Action: "bottom", Keys: []string{"end"}, Ctx: ctxList, Group: "이동", Help: "맨 아래", NoPalette: true}, "list.move"),
		section(keys.Binding{Action: "halfdown", Keys: []string{"ctrl+d"}, Ctx: ctxList, Group: "이동", Help: "반 페이지 아래", NoPalette: true}, "list.move"),
		section(keys.Binding{Action: "halfup", Keys: []string{"ctrl+u"}, Ctx: ctxList, Group: "이동", Help: "반 페이지 위", NoPalette: true}, "list.move"),
		short(b("select", []string{" "}, ctxList, "선택", "선택 토글", 4), "선택"),
		b("selectall", []string{"*"}, ctxList, "선택", "보이는 항목 모두 선택", 14),
		b("clear", []string{"esc"}, ctxList, "선택", "선택·검색 해제", 15),

		short(b("group.enter", []string{"enter"}, ctxGroups, "그룹", "전환 (tmux window, 없으면 Chrome 그룹)", 1), "전환"),
		short(b("group.new", []string{"n"}, ctxGroups, "그룹", "새 그룹 (선택한 탭을 넣어서)", 5), "새 그룹"),
		b("group.rename", []string{"r"}, ctxGroups, "그룹", "이름 바꾸기", 5),
		b("group.color", []string{"c"}, ctxGroups, "그룹", "색 바꾸기", 7),
		b("group.bind", []string{"b"}, ctxGroups, "그룹", "현재 tmux window에 바인딩", 8),
		upper(b("group.merge", []string{"M"}, ctxGroups, "그룹", "다른 그룹으로 합치기", 9)),
		short(b("group.ungroup", []string{"d"}, ctxGroups, "그룹", "그룹 해제 (탭은 남김)", 6), "해제"),
		upper(b("group.close", []string{"D"}, ctxGroups, "그룹", "그룹과 탭 모두 닫기", 10)),
		b("group.open", []string{"o"}, ctxGroups, "그룹", "이 그룹에 URL 열기", 10),
		b("group.grab", []string{"g"}, ctxGroups, "그룹", "pane의 URL 가져오기", 11),

		short(b("tab.focus", []string{"enter"}, ctxTabs, "탭", "탭으로 이동 (터미널 포커스 유지)", 1), "이동"),
		b("tab.move", []string{"m"}, ctxTabs, "탭", "다른 그룹으로 이동", 5),
		b("tab.ungroup", []string{"u"}, ctxTabs, "탭", "그룹에서 빼기", 7),
		b("tab.close", []string{"x"}, ctxTabs, "탭", "닫기", 5),
		b("tab.pin", []string{"p"}, ctxTabs, "탭", "고정 토글", 8),
		upper(b("tab.reload", []string{"R"}, ctxTabs, "탭", "새로고침", 10)),
		b("tab.copy", []string{"y"}, ctxTabs, "탭", "URL 복사", 9),
		b("tab.new", []string{"n"}, ctxTabs, "탭", "새 그룹 (선택한 탭을 넣어서)", 6),
		b("tab.open", []string{"o"}, ctxTabs, "탭", "이 그룹에 URL 열기", 10),
		b("tab.grab", []string{"g"}, ctxTabs, "탭", "pane의 URL 가져오기", 11),

		// The search field owns every printable key; only these act.
		b("search.open", []string{"enter"}, ctxSearch, "검색", "열기", 1),
		b("search.exit", []string{"esc"}, ctxSearch, "검색", "검색 끝내기 (조건 유지)", 2),
		move(b("search.down", []string{"down", "ctrl+n", "ctrl+j"}, ctxSearch, "검색", "아래", 3), "이동"),
		move(b("search.up", []string{"up", "ctrl+p"}, ctxSearch, "검색", "위", 3), "이동"),
		b("search.scope", []string{"tab"}, ctxSearch, "검색", "범위", 4),
		b("search.select", []string{"ctrl+t"}, ctxSearch, "검색", "선택 토글", 5),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxSearch, Help: "종료", Pinned: true, Footer: 20},

		keys.Binding{Action: "chrome.launch", Keys: []string{"enter"}, Ctx: ctxHalted, Group: "연결", Help: "Chrome 실행",
			Footer: 1, Shadows: []string{"group.enter", "tab.focus"}},

		b("confirm.yes", []string{"y", "enter"}, ctxConfirm, "확인", "실행", 1),
		b("confirm.no", []string{"n", "esc", "q"}, ctxConfirm, "확인", "취소", 2),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxConfirm, Help: "종료", Pinned: true, Footer: 20},

		b("prompt.ok", []string{"enter"}, ctxPrompt, "입력", "확인", 1),
		b("prompt.cancel", []string{"esc"}, ctxPrompt, "입력", "취소", 2),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxPrompt, Help: "종료", Pinned: true, Footer: 20},

		b("picker.ok", []string{"enter"}, ctxPicker, "고르기", "고르기", 1),
		b("picker.cancel", []string{"esc"}, ctxPicker, "고르기", "취소", 2),
		move(b("picker.down", []string{"down", "ctrl+n", "ctrl+j"}, ctxPicker, "고르기", "아래", 3), "이동"),
		move(b("picker.up", []string{"up", "ctrl+p"}, ctxPicker, "고르기", "위", 3), "이동"),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxPicker, Help: "종료", Pinned: true, Footer: 20},

		b("check.toggle", []string{" "}, ctxChecklist, "체크리스트", "체크 토글", 2),
		b("check.all", []string{"a"}, ctxChecklist, "체크리스트", "모두 체크/해제", 3),
		b("check.ok", []string{"enter"}, ctxChecklist, "체크리스트", "체크한 항목 실행", 1),
		b("check.cancel", []string{"esc", "q"}, ctxChecklist, "체크리스트", "취소", 4),
		move(b("check.down", []string{"j", "down"}, ctxChecklist, "체크리스트", "아래", 5), "이동"),
		move(b("check.up", []string{"k", "up"}, ctxChecklist, "체크리스트", "위", 5), "이동"),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxChecklist, Help: "종료", Pinned: true, Footer: 20},

		b("help.close", []string{"esc", "q", "?"}, ctxHelp, "도움말", "닫기", 1),
		move(b("help.down", []string{"j", "down"}, ctxHelp, "도움말", "아래로", 2), "스크롤"),
		move(b("help.up", []string{"k", "up"}, ctxHelp, "도움말", "위로", 2), "스크롤"),
		keys.Binding{Action: "quit", Keys: []string{"ctrl+c"}, Ctx: ctxHelp, Help: "종료", Pinned: true, Footer: 20},
	)
}
