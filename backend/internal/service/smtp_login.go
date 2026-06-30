package service

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/textproto"
	"strings"
)

// sendMailWithLogin 使用 SMTP LOGIN 认证发送邮件
func sendMailWithLogin(addr, from string, to []string, msg []byte, username, password string, useTLS bool) error {
	from = strings.TrimSpace(from)
	if from == "" {
		return fmt.Errorf("from address is empty")
	}

	validTo := make([]string, 0, len(to))
	for _, t := range to {
		t = strings.TrimSpace(t)
		if t != "" {
			validTo = append(validTo, t)
		}
	}
	if len(validTo) == 0 {
		return fmt.Errorf("to addresses are all empty")
	}

	// 1. 建立 TCP 连接
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp failed: %w", err)
	}
	defer conn.Close()

	// 2. 建立 textproto 读写通道
	text := textproto.NewConn(conn)
	defer text.Close()

	// 3. 读 220 欢迎消息
	if _, _, err := text.ReadResponse(220); err != nil {
		return fmt.Errorf("smtp welcome failed: %w", err)
	}

	host := strings.Split(addr, ":")[0]

	// 4. EHLO
	if _, err := text.Cmd("EHLO %s", "localhost"); err != nil {
		return fmt.Errorf("ehlo failed: %w", err)
	}
	for {
		line, err := text.ReadLine()
		if err != nil {
			return fmt.Errorf("ehlo read failed: %w", err)
		}
		if !strings.HasPrefix(line, "250-") {
			// 最后一行是 250 (无连字符)
			break
		}
	}

	// 5. STARTTLS（如果需要且服务器支持）
	if useTLS {
		if _, err := text.Cmd("STARTTLS"); err != nil {
			return fmt.Errorf("starttls command failed: %w", err)
		}
		if _, _, err := text.ReadResponse(220); err != nil {
			return fmt.Errorf("starttls response failed: %w", err)
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, InsecureSkipVerify: false})
		if err := tlsConn.Handshake(); err != nil {
			return fmt.Errorf("tls handshake failed: %w", err)
		}
		text = textproto.NewConn(tlsConn)
		// 重新 EHLO
		if _, err := text.Cmd("EHLO %s", "localhost"); err != nil {
			return fmt.Errorf("ehlo after tls failed: %w", err)
		}
		for {
			line, err := text.ReadLine()
			if err != nil {
				return fmt.Errorf("ehlo after tls read failed: %w", err)
			}
			if !strings.HasPrefix(line, "250-") {
				break
			}
		}
	}

	// 6. AUTH LOGIN
	if username != "" {
		if _, err := text.Cmd("AUTH LOGIN"); err != nil {
			return fmt.Errorf("auth login command failed: %w", err)
		}
		if _, _, err := text.ReadResponse(334); err != nil {
			return fmt.Errorf("auth login challenge failed: %w", err)
		}
		// 用户名
		if err := text.PrintfLine(base64.StdEncoding.EncodeToString([]byte(username))); err != nil {
			return fmt.Errorf("send username failed: %w", err)
		}
		if _, _, err := text.ReadResponse(334); err != nil {
			return fmt.Errorf("username challenge failed: %w", err)
		}
		// 密码
		if err := text.PrintfLine(base64.StdEncoding.EncodeToString([]byte(password))); err != nil {
			return fmt.Errorf("send password failed: %w", err)
		}
		if _, _, err := text.ReadResponse(235); err != nil {
			return fmt.Errorf("password response failed: %w", err)
		}
	}

	// 7. MAIL FROM
	if _, err := text.Cmd("MAIL FROM:<%s>", from); err != nil {
		return fmt.Errorf("mail from command failed: %w", err)
	}
	if _, _, err := text.ReadResponse(250); err != nil {
		return fmt.Errorf("mail from response failed: %w", err)
	}

	// 8. RCPT TO
	for _, r := range validTo {
		if _, err := text.Cmd("RCPT TO:<%s>", r); err != nil {
			return fmt.Errorf("rcpt to command failed: %w", err)
		}
		if _, _, err := text.ReadResponse(250); err != nil {
			return fmt.Errorf("rcpt to response failed: recipient=%s, err=%w", r, err)
		}
	}

	// 9. DATA
	if _, err := text.Cmd("DATA"); err != nil {
		return fmt.Errorf("data command failed: %w", err)
	}
	if _, _, err := text.ReadResponse(354); err != nil {
		return fmt.Errorf("data response failed: %w", err)
	}

	// 10. 发送消息正文，以 \r\n.\r\n 结尾
	w := text.DotWriter()
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer failed: %w", err)
	}

	// 11. QUIT
	if _, err := text.Cmd("QUIT"); err != nil {
		return fmt.Errorf("quit failed: %w", err)
	}
	return nil
}
