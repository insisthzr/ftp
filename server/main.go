package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
)

func getAddrFromFTP(address string) (string, error) {
	var a, b, c, d byte
	var p1, p2 int
	_, err := fmt.Sscanf(address, "%d,%d,%d,%d,%d,%d", &a, &b, &c, &d, &p1, &p2)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d.%d:%d", a, b, c, d, 256*p1+p2), nil
}

func NewConnection(conn net.Conn) *Connection {
	dir, _ := os.Getwd()
	return &Connection{rw: conn, dir: dir}
}

type Connection struct {
	cmdErr   error
	rw       net.Conn
	prevCmd  string
	dataAddr string
	dir      string
	isBinary bool
}

func (c *Connection) writeln(args ...interface{}) error {
	args = append(args, "\r\n")
	_, err := fmt.Fprint(c.rw, args...)
	if err != nil {
		log.Println(err)
	}
	return err
}

func (c *Connection) quit() {
	c.writeln("221 Goodbye.")
}

func (c *Connection) user() {
	c.writeln("230 Login successful.")
}

func (c *Connection) port(args []string) {
	if len(args) != 1 {
		c.writeln("501 Usage: PORT a,b,c,d,p1,p2")
		return
	}
	addr, err := getAddrFromFTP(args[0])
	if err != nil {
		c.writeln("501 %v", err)
		return
	}
	c.dataAddr = addr
	c.writeln("200 PORT command successful.")
}

func (c *Connection) list(args []string) {
	if len(args) > 1 {
		c.writeln("501 ")
		return
	}
	filename := c.dir
	if len(args) == 1 {
		filename = path.Join(filename, args[0])
	}
	wrc, err := c.dataConn()
	if err != nil {
		c.writeln("425 Can't open data connection.")
	}
	defer wrc.Close()

	//TODO: implement cmd ls -l
	cmd := exec.Command("ls", "-l", filename)
	out := &bytes.Buffer{}
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		log.Println(err)
	}
	c.writeln("150 Here comes the directory listing.")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for _, line := range lines {
		fmt.Fprintf(wrc, "%s\r\n", line)
	}
	c.writeln("226 Closing data connection. List successful.")
}

func (c *Connection) cwd(args []string) {
	if len(args) > 1 {
		c.writeln("501 ")
	}
	dir := c.dir
	if len(args) == 1 {
		dir = path.Join(dir, args[0])
	}
	c.dir = dir
	c.writeln("250 Directory successfully changed.")
}

func (c *Connection) retr(args []string) {
	if len(args) != 1 {
		c.writeln("501 error")
		return
	}
	rwc, err := c.dataConn()
	if err != nil {
		c.writeln("450", err.Error())
		return
	}
	defer rwc.Close()
	filePath := path.Join(c.dir, args[0])
	file, err := os.Open(filePath)
	if err != nil {
		c.writeln("501 ", err.Error())
		return
	}
	defer file.Close()
	c.writeln("150 File ok. Sending.")
	if c.isBinary {
		_, err = io.Copy(rwc, file)
		if err != nil {
			c.writeln("450", err.Error())
			return
		}
	} else {
		r := bufio.NewReader(file)
		w := bufio.NewWriter(rwc)
		for {
			line, ok, err := r.ReadLine()
			if err != nil {
				if err == io.EOF {
					break
				} else {
					c.writeln("450", err.Error())
					return
				}
			}
			w.Write(line)
			if !ok {
				w.WriteString("\r\n")
			}
		}
		w.Flush()
	}
	c.writeln("226 Transfer complete.")
}

func (c *Connection) stor(args []string) {
	if len(args) != 1 {
		c.writeln("501 error")
		return
	}

	rwc, err := c.dataConn()
	if err != nil {
		c.writeln("501 %s", err.Error())
		return
	}
	defer rwc.Close()

	filePath := path.Join(c.dir, args[0])
	file, err := os.Create(filePath)
	if err != nil {
		c.writeln("501 %s", err.Error())
		return
	}
	defer file.Close()

	c.writeln("150 Ok to send data.")
	if c.isBinary {
		_, err = io.Copy(file, rwc)
		if err != nil {
			c.writeln("501 %s", err.Error())
			return
		}
	} else {
		r := bufio.NewReader(rwc)
		w := bufio.NewWriter(file)
		for {
			line, ok, err := r.ReadLine()
			if err != nil {
				if err == io.EOF {
					break
				} else {
					c.writeln("450", err.Error())
					return
				}
			}
			w.Write(line)
			if !ok {
				w.WriteString("\r\n")
			}
		}
		w.Flush()
	}

	c.writeln("226 Transfer complete.")
}

func (c *Connection) typ(args []string) {
	if len(args) < 1 || len(args) > 2 {
		c.writeln("501 Usage: TYPE takes 1 or 2 arguments.")
		return
	}
	switch strings.ToUpper(strings.Join(args, " ")) {
	case "A", "A N":
		c.isBinary = false
	case "I", "L 8":
		c.isBinary = true
	default:
		c.writeln("504 Unsupported type. Supported types: A, A N, I, L 8.")
		return
	}
	c.writeln("200 TYPE set")
}

func (c *Connection) syst() {
	c.writeln("215 UNIX Type: L8")
}

func (c *Connection) noop() {
	c.writeln("200 Ready.")
}

func (c *Connection) dataConn() (io.ReadWriteCloser, error) {
	var conn io.ReadWriteCloser
	var err error
	switch c.prevCmd {
	case "PORT":
		conn, err = net.Dial("tcp4", c.dataAddr)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("previous command not PORT")
	}
	return conn, nil
}

func (c *Connection) Run() {
	log.Println("client connected")
	c.writeln("200 Ready.")
	scanner := bufio.NewScanner(c.rw)
	for scanner.Scan() {
		if c.cmdErr != nil {
			log.Println(c.cmdErr)
			break
		}
		text := scanner.Text()
		log.Println(text)
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		cmd := strings.ToUpper(fields[0])
		args := []string{}
		if len(fields) > 1 {
			args = fields[1:]
		}
		switch cmd {
		case "QUIT":
			c.quit()
			break
		case "USER":
			c.user()
		case "PORT":
			c.port(args)
		case "LIST":
			c.list(args)
		case "CWD":
			c.cwd(args)
		case "RETR":
			c.retr(args)
		case "STOR":
			c.stor(args)
		case "SYST":
			c.syst()
		case "NOOP":
			c.noop()
		case "TYPE":
			c.typ(args)
		default:
			c.writeln(fmt.Sprintf("502 Command %q not implemented.", cmd))
		}
		c.prevCmd = cmd
	}
	log.Println("client closed")
}

var (
	port = flag.String("port", "20001", "port")
)

func main() {
	flag.Parse()
	listener, err := net.Listen("tcp4", ":"+*port)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("listening at %s\n", *port)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		c := NewConnection(conn)
		go c.Run()
	}
}
