package safestorage

import (
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpStorage struct {
	cfg    Config
	client *sftp.Client
	ssh    *ssh.Client
}

func newSFTP(cfg Config) *sftpStorage {
	return &sftpStorage{cfg: cfg}
}

func (s *sftpStorage) Connect() error {
	port := s.cfg.Port
	if port == 0 {
		port = 22
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, port)

	config := &ssh.ClientConfig{
		User:            s.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(s.cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return err
	}

	client, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return err
	}

	s.client = client
	s.ssh = sshClient
	return nil
}

func (s *sftpStorage) Close() error {
	var err1, err2 error
	if s.client != nil {
		err1 = s.client.Close()
	}
	if s.ssh != nil {
		err2 = s.ssh.Close()
	}
	if err1 != nil {
		return err1
	}
	return err2
}

func (s *sftpStorage) RemoteSize(remotePath string) (int64, error) {
	info, err := s.client.Stat(remotePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *sftpStorage) Upload(localPath, remotePath string, offset int64, progress func(sent, total int64)) error {
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()

	st, err := local.Stat()
	if err != nil {
		return err
	}
	total := st.Size()

	if offset > 0 {
		if _, err := local.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}

	if err := s.client.MkdirAll(path.Dir(remotePath)); err != nil {
		return err
	}

	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	remote, err := s.client.OpenFile(remotePath, flags)
	if err != nil {
		return err
	}
	defer remote.Close()

	sent := offset
	if progress != nil {
		progress(sent, total)
	}

	buf := make([]byte, 256*1024)
	for {
		n, err := local.Read(buf)
		if n > 0 {
			_, werr := remote.Write(buf[:n])
			if werr != nil {
				return werr
			}
			sent += int64(n)
			if progress != nil {
				progress(sent, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}
