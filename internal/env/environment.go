package env

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sbreitf1/go-console"
	"github.com/sbreitf1/go-jcrypt"
)

const (
	envOrderFileName = "env-order"
)

type envOrder struct {
	Fixed bool     `json:"fixed"`
	Order []string `json:"order"`
}

// GetKeyHandler returns the encryption key for encryption or decryption.
type GetKeyHandler jcrypt.KeySource

// EnterEnvHandler prepares the environment defined by env.
type EnterEnvHandler func(envName string, env interface{}) error

// GetEnvFileTitleHandler returns a humand readable title for the given env file.
type GetEnvFileTitleHandler func(envName, envFile string) string

// Reader represents a reader object for environments.
type Reader struct {
	dir             string
	KeySource       GetKeyHandler
	EnterEnvHandler EnterEnvHandler
	GetEnvFileTitle GetEnvFileTitleHandler
}

// Dir returns the configuration directory.
func (e *Reader) Dir() string {
	return e.dir
}

// NewReader returns a new environment reader using the given key source and enter environment handler.
func NewReader(homeDirName string) (*Reader, error) {
	dir, err := getConfigDir()
	if err != nil {
		return nil, err
	}

	return &Reader{dir: filepath.Join(dir, homeDirName)}, nil
}

func (e *Reader) getEnvFilePath(envName string) string {
	//TODO conditional escaping
	return filepath.Join(e.dir, envName+".json")
}

// ReadEnvironment reads an existing environment.
func (e *Reader) ReadEnvironment(envName string, env interface{}) error {
	return e.createOrReadEnvironment(envName, env, nil)
}

// CreateOrReadEnvironment reads an existing environment with the given name or calls the enter environment reader.
func (e *Reader) CreateOrReadEnvironment(envName string, env interface{}) error {
	return e.createOrReadEnvironment(envName, env, e.EnterEnvHandler)
}

func (e *Reader) createOrReadEnvironment(envName string, env interface{}, enterEnvHandler EnterEnvHandler) error {
	file := e.getEnvFilePath(envName)
	exists, err := isFile(file)
	if err != nil {
		return err
	}

	keySource := jcrypt.KeySource(e.KeySource)
	if keySource == nil {
		keySource = func() ([]byte, error) { return []byte{}, nil }
	}

	if !exists {
		if enterEnvHandler != nil {
			console.Printlnf("Environment %q does not exist yet, pleaser enter below:", envName)
			err := enterEnvHandler(envName, env)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(e.dir, os.ModePerm); err != nil {
				console.Printlnf("WARNING: failed to save environment: %s", err.Error())
			} else {
				if err := jcrypt.MarshalToFile(file, env, &jcrypt.Options{
					GetKeyHandler: keySource,
				}); err != nil {
					console.Printlnf("WARNING: failed to save environment: %s", err.Error())
				}
			}

			e.envOrderBringToFront(envName)
			return nil
		}
		return fmt.Errorf("environment %q not found", envName)
	}

	if err := jcrypt.UnmarshalFromFile(file, env, &jcrypt.Options{
		GetKeyHandler: keySource,
	}); err != nil {
		return err
	}
	e.envOrderBringToFront(envName)
	return nil
}

// SelectEnvironment displays all configured environments in specified order and prompts the user.
func (e *Reader) SelectEnvironment(env interface{}) error {
	envFiles, err := e.GetEnvironmentFiles()
	if err != nil {
		return err
	}
	if len(envFiles) == 0 {
		return fmt.Errorf("no environments specified")
	}

	envTitles := make([]string, len(envFiles))
	for i, fi := range envFiles {
		name := fi.Name()
		if strings.HasSuffix(name, ".json") {
			name = name[:len(name)-5]
		}

		if e.GetEnvFileTitle != nil {
			envTitles[i] = e.GetEnvFileTitle(name, filepath.Join(e.dir, fi.Name()))
		} else {
			envTitles[i] = name
		}
	}
	ui := promptui.Select{Label: "Select environment", Items: envTitles, HideSelected: true}
	index, _, err := ui.Run()
	if err != nil {
		return err
	}

	fileName := envFiles[index].Name()
	return e.createOrReadEnvironment(fileName[:len(fileName)-5], env, nil)
}

// ListEnvironments returns a list of all environment titles.
func (e *Reader) ListEnvironments() ([]string, error) {
	envFiles, err := e.GetEnvironmentFiles()
	if err != nil {
		return nil, err
	}
	if len(envFiles) == 0 {
		return []string{}, nil
	}

	envTitles := make([]string, len(envFiles))
	for i, fi := range envFiles {
		name := fi.Name()
		if strings.HasSuffix(name, ".json") {
			name = name[:len(name)-5]
		}

		if e.GetEnvFileTitle != nil {
			envTitles[i] = e.GetEnvFileTitle(name, filepath.Join(e.dir, fi.Name()))
		} else {
			envTitles[i] = name
		}
	}
	return envTitles, nil
}

// GetEnvironmentFiles returns an ordered list of files that contain environments.
func (e *Reader) GetEnvironmentFiles() ([]os.FileInfo, error) {
	files, err := ioutil.ReadDir(e.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []os.FileInfo{}, nil
		}
		return nil, err
	}

	envFiles := make([]os.FileInfo, 0)
	for _, fi := range files {
		if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".json") && fi.Name() != envOrderFileName {
			envFiles = append(envFiles, fi)
		}
	}

	order, _ := e.readEnvOrder()
	if order.Order != nil && len(order.Order) > 0 {
		orderMap := make(map[string]int)
		for i, name := range order.Order {
			orderMap[name] = i
		}
		sort.SliceStable(envFiles, func(i, j int) bool {
			iVal, iOk := orderMap[envFiles[i].Name()]
			jVal, jOk := orderMap[envFiles[j].Name()]
			if !iOk {
				iVal = math.MaxInt32
			}
			if !jOk {
				jVal = math.MaxInt32
			}
			return iVal < jVal
		})
	}

	return envFiles, nil
}

func (e *Reader) readEnvOrder() (envOrder, error) {
	orderData, err := ioutil.ReadFile(filepath.Join(e.dir, envOrderFileName))
	if err != nil {
		return envOrder{false, []string{}}, nil
	}

	var order envOrder
	if err := json.Unmarshal(orderData, &order); err != nil {
		return envOrder{false, []string{}}, nil
	}

	return order, nil
}

func (e *Reader) writeEnvOrder(order envOrder) error {
	orderData, err := json.Marshal(&order)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filepath.Join(e.dir, envOrderFileName), orderData, os.ModePerm)
}

func (e *Reader) envOrderBringToFront(name string) error {
	order, err := e.readEnvOrder()
	if err != nil {
		return err
	}

	if order.Fixed {
		return nil
	}

	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	if order.Order == nil {
		order.Order = []string{name}
	} else {
		newOrder := []string{name}
		for _, env := range order.Order {
			if env != name {
				newOrder = append(newOrder, env)
			}
		}
		order.Order = newOrder
	}

	return e.writeEnvOrder(order)
}

// DeleteEnvironment deletes an existing environment.
func (e *Reader) DeleteEnvironment(envName string) error {
	file := e.getEnvFilePath(envName)
	exists, err := isFile(file)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("environment %q does not exist", envName)
	}

	return os.Remove(file)
}

func getConfigDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return usr.HomeDir, nil
}

func isFile(file string) (bool, error) {
	fi, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !fi.IsDir(), nil
}
