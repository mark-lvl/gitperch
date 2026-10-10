package github

import (
	"os"
	"os/exec"
	"strconv"
)

// BrowseCommand opens a pull request (number > 0) or the repository, at
// branch when one is given, in the browser gh is set up to use. The caller
// runs it with the terminal handed over, since BROWSER may name a terminal
// browser.
func (r Runner) BrowseCommand(repo Repo, number int, branch string) (*exec.Cmd, error) {
	bin, err := r.executable()
	if err != nil {
		return nil, err
	}
	args := []string{"browse"}
	if number > 0 {
		args = append(args, strconv.Itoa(number))
	}
	args = append(args, "--repo="+repo.String())
	if number == 0 && branch != "" {
		args = append(args, "--branch="+branch)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = environment(os.Environ())
	return cmd, nil
}
