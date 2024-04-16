package main

import (
	"os"

	"github.com/samber/lo"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/kustomize/api/types"
)

const (
	k8sKustomizeFile = "k8s/overlays/okteto-dev/images.yaml"
)

type k8sRenameInfo struct {
	name   string
	image  string
	digest string
}

func getKustomiztion() (*types.Kustomization, error) {
	data, err := os.ReadFile(k8sKustomizeFile)
	if os.IsNotExist(err) {
		return &types.Kustomization{}, nil
	}
	if err != nil {
		return nil, err
	}

	var k types.Kustomization
	if err := k.Unmarshal(data); err != nil {
		return nil, err
	}

	return &k, nil
}

func updateK8sDeployment(k8sInfoChan chan *k8sRenameInfo) error {
	infos := map[string]*k8sRenameInfo{}
	for info := range k8sInfoChan {
		infos[info.name] = info
	}

	k, err := getKustomiztion()
	if err != nil {
		return err
	}

	for service, info := range infos {
		fullName := "registry.lema.ai/" + service

		_, idx, ok := lo.FindIndexOf(k.Images, func(i types.Image) bool {
			return i.Name == fullName
		})

		if !ok {
			k.Images = append(k.Images, types.Image{
				Name:    fullName,
				NewName: info.image,
				Digest:  info.digest,
			})
		} else {
			k.Images[idx].NewName = info.image
			k.Images[idx].Digest = info.digest
		}
	}

	out, err := yaml.Marshal(k)
	if err != nil {
		return err
	}

	return os.WriteFile(k8sKustomizeFile, out, 0644)
}
