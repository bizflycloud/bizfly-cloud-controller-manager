package framework

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"time"
)

const (
	scriptDirectory = "scripts"
	RetryInterval   = 5 * time.Second
	RetryTimeout    = 15 * time.Minute
)

func RunScript(script string, args ...string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	return runCommand(path.Join(wd, scriptDirectory, script), args...)
}

func runCommand(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	log.Printf("Running command %q\n", cmd)
	return c.Run()
}

func deleteInForeground() metav1.DeleteOptions {
	policy := metav1.DeletePropagationForeground
	graceSeconds := int64(0)
	return metav1.DeleteOptions{
		PropagationPolicy:  &policy,
		GracePeriodSeconds: &graceSeconds,
	}
}

func characters(length int) string {
	// Create a new rand.Source seeded with the current time
	randSource := rand.NewSource(time.Now().UnixNano())

	// Create a new rand.Rand instance using the randSource
	randGenerator := rand.New(randSource)

	charset := "abcdefghijklmnopqrstuvwxyz0123456789"

	// Generate a random string of length 5
	k8sName := make([]byte, length)
	for i := range k8sName {
		k8sName[i] = charset[randGenerator.Intn(len(charset))]
	}
	return string(k8sName)
}

func GetResponseFromCurl(endpoint string) string {
	resp, err := exec.Command("curl", "--max-time", "5", "-s", endpoint).Output()
	if err != nil {
		return ""
	}
	return string(resp)
}

func cutString(original string) string {
	if len(original) > 255 {
		original = original[:255]
	}
	return original
}
