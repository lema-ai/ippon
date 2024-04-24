package main

import (
	"os"
	"path"

	"github.com/samber/lo"
	"gopkg.in/yaml.v2"
)

type Images struct {
	Images []*Image `yaml:"images"`
}

type Image struct {
	OldName string `yaml:"old_image"`
	NewName string `yaml:"new_image"`
}

func getKustomiztion(path string) (*Images, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var i Images
	err = yaml.Unmarshal(data, &i)
	if err != nil {
		return nil, err
	}

	return &i, nil
}

func updateK8sDeployment(namespace string, imagesChan chan *Image) error {
	builtImages := []*Image{}
	for image := range imagesChan {
		builtImages = append(builtImages, image)
	}

	filePath := path.Join(".ippon", namespace+".yaml")
	images, err := getKustomiztion(filePath)
	if err != nil {
		return err
	}

	currentImages := []*Image{}
	if images != nil && images.Images != nil {
		currentImages = images.Images
	}

	for _, image := range builtImages {
		_, idx, ok := lo.FindIndexOf(currentImages, func(i *Image) bool {
			return i.OldName == image.OldName
		})

		if !ok {
			currentImages = append(currentImages, image)
		} else {
			currentImages[idx].NewName = image.NewName
		}
	}

	images = &Images{Images: currentImages}

	out, err := yaml.Marshal(images)
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, out, 0644)
}
