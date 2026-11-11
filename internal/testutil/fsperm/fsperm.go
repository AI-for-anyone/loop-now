// Package fsperm 给测试用：把目录或文件放开给所有人，用来核对被测代码确实会把它收紧。
// Windows 的临时目录在用户目录下，本来就只有当前用户能访问，不先放开就分不出是代码收紧的还是从上级继承来的。
package fsperm

import "testing"

// Open 让 path 对所有人可访问：Unix 上目录 0777、文件 0666；Windows 上加一条允许 Everyone 完全访问的 ACE，
// 目录上的这条可继承，之后在其中新建的东西也对所有人可访问。
func Open(t testing.TB, path string) {
	t.Helper()
	if err := open(path); err != nil {
		t.Fatalf("放开 %s 的权限：%v", path, err)
	}
}
