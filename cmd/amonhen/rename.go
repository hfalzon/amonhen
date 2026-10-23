package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

func renameProject(args []string) error {
	if containsHelp(args) {
		fmt.Println("Usage: amonhen project rename -N <name> --new-name <name>\n\nRename a project and preserve its stored credential.")
		return nil
	}
	opts, err := parseOptions(args, map[string]bool{"name": true, "new-name": true})
	if err != nil {
		return err
	}
	if !validName(opts["name"]) || !validName(opts["new-name"]) {
		return errors.New("valid -N and --new-name values are required")
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	index := -1
	for i, p := range projects {
		if strings.EqualFold(p.Name, opts["new-name"]) {
			return errors.New("new project name already exists")
		}
		if strings.EqualFold(p.Name, opts["name"]) {
			index = i
		}
	}
	if index < 0 {
		return errors.New("project not found")
	}
	old, name := projects[index].Name, opts["new-name"]
	if _, err := keyring.Get(service, name); !errors.Is(err, keyring.ErrNotFound) {
		return errors.New("destination credential already exists or cannot be inspected; rename refused")
	}
	token, err := keyring.Get(service, old)
	if err != nil {
		return projectTokenReadError(old, err)
	}
	if err := keyring.Set(service, name, token); err != nil {
		return errors.New("cannot create renamed OS credential; registry unchanged")
	}
	projects[index].Name = name
	if err := saveProjects(projects); err != nil {
		if rollback := keyring.Delete(service, name); rollback != nil {
			return fmt.Errorf("%w; could not remove unused destination credential", err)
		}
		return err
	}
	if err := keyring.Delete(service, old); err != nil {
		return errors.New("project renamed, but unused old credential could not be removed")
	}
	fmt.Printf("Renamed project %s to %s; stored credential preserved.\n", old, name)
	return nil
}
