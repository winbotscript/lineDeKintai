package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/yuki9431/autoNetDeKintai/component"
	"github.com/yuki9431/logger"
	"github.com/yuki9431/mongoHelper"

	"github.com/globalsign/mgo/bson"
	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/line/line-bot-sdk-go/linebot/httphandler"
)

const (
	logfile    = "/var/log/lineDeKintai.log"
	configFile = "config.json"
	mongoDial  = "mongodb://localhost/mongodb"
	mongoName  = "lineDeKintai"
	imageUrl   = "https://blacksnowpi.f5.si/image/dakoku.png"
	usage      = `機能説明`
)

// ユーザプロフィール情報
type UserInfo struct {
	UserID       string `json:"userId"`
	DisplayName  string `json:"displayName"`
	NetDeKomonId string `json:"netDekomonId"`
	Password     string `json:"password"`
	IsCome       bool   `json:"isCome"`
}

// API等の設定
type ApiIds struct {
	ChannelSecret string `json:"channelSecret"`
	ChannelToken  string `json:"channelToken"`
	AppId         string `json:"appId"`
	CityId        string `json:"cityId"`
	CertFile      string `json:"certFile"`
	KeyFile       string `json:"keyFile"`
}

func main() {
	// log出力設定
	file, err := os.OpenFile(logfile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	logger := logger.New(file)

	// 設定ファイル読み込み
	apiIds := new(ApiIds)
	config := NewConfig(configFile)
	if err := config.Read(apiIds); err != nil {
		logger.Fatal(err)
	}

	handler, err := httphandler.New(apiIds.ChannelSecret, apiIds.ChannelToken)
	if err != nil {
		logger.Fatal(err)
	}

	// Setup HTTP Server for receiving requests from LINE platform
	handler.HandleEvents(func(events []*linebot.Event, r *http.Request) {
		bot, err := handler.NewClient()
		if err != nil {
			logger.Fatal(err)
			return
		}

		// イベント処理
		for _, event := range events {
			// DB設定
			mongo, err := mongoHelper.NewMongo(mongoDial, mongoName)
			if err != nil {
				logger.Fatal(err)
			}

			logger.Write("start event : " + event.Type)

			// ユーザのIDを取得
			userId := event.Source.UserID
			logger.Write("userid :" + userId)

			// ユーザのプロフィールを取得後、レスポンスする
			if profile, err := bot.GetProfile(userId).Do(); err == nil {
				if event.Type == linebot.EventTypeMessage {
					// 返信メッセージ
					var replyMessage string

					switch message := event.Message.(type) {
					case *linebot.TextMessage:
						if strings.Contains(message.Text, "出社") || strings.Contains(message.Text, "退社") {
							logger.Write("打刻処理開始")

							// 打刻用のパラメータ
							var isCome bool
							var punchMessage string
							var userInfos []UserInfo

							// 打刻処理
							if err := mongo.SearchDb(&userInfos, bson.M{"userid": userId}, "userInfos"); err == nil {
								var kintaiInfo component.User

								// スライス型のためループするが、１つしかデータを取得できない想定
								for _, userInfo := range userInfos {
									kintaiInfo.Id = userInfo.NetDeKomonId
									kintaiInfo.Password = userInfo.Password
								}

								// 出社・退社の判定を行う 処理が不正の場合は、打刻処理を実施しない
								punchFlag := false
								if strings.Contains(message.Text, "出社") {
									if userInfos[0].IsCome == false {
										punchFlag = true
										isCome = true
										punchMessage = "出社"

										// DBのフラグを反転
										selector := bson.M{"userid": profile.UserID}
										update := bson.M{"$set": bson.M{"iscome": isCome}}

										if err := mongo.UpdateDb(selector, update, "userInfos"); err != nil {
											logger.Write("failed netdekomonid update")

										}
									} else {
										replyMessage = "1日に何回も出社しなくていいです"
									}
								} else if strings.Contains(message.Text, "退社") {
									if userInfos[0].IsCome == true {
										punchFlag = true
										isCome = false
										punchMessage = "退社"

										// DBのフラグを反転
										selector := bson.M{"userid": profile.UserID}
										update := bson.M{"$set": bson.M{"iscome": isCome}}

										if err := mongo.UpdateDb(selector, update, "userInfos"); err != nil {
											logger.Write("failed netdekomonid update")

										}

									} else {
										replyMessage = "退社済みですよ？"
									}
								} else {
									replyMessage = "隠し機能はありません。"
								}

								// 実際の打刻処理
								if kintaiInfo.Id == "" || kintaiInfo.Password == "" {
									replyMessage = "Error: ログインID・パスワードが登録されていません。\n" +
										"登録してからご利用ください。\n" +
										"下記のメッセージを送信すると登録できます。\n\n" +
										"ログインID:<loginId>\n\n" +
										"パスワード:<password>"

								} else if punchFlag {
									if err := component.Punch(kintaiInfo, isCome); err != nil {
										replyMessage = punchMessage + "しました"

										// TODO 打刻時間を画像で送る
										// if _, err := bot.ReplyMessage(event.ReplyToken, linebot.NewImageMessage(imageUrl, imageUrl)).Do(); err != nil {
										// 	logger.Write(err)
										// }
									}
								}
							} else {
								logger.Write("DB抽出処理失敗　:" + err.Error())
								replyMessage = punchMessage + "に失敗しました\n" +
									"Error: " + err.Error()
							}

							logger.Write("打刻処理終了")

						} else if strings.Contains(message.Text, "ログインID:") {
							loginId := strings.Replace(message.Text, " ", "", -1) // 全ての半角を消す
							loginId = strings.Replace(loginId, "ログインID:", "", 1)  // 頭のログインID:を消す

							// ネットDe勤怠のIDをDBに登録する
							if loginId != "" {
								// DB登録処理
								selector := bson.M{"userid": profile.UserID}
								update := bson.M{"$set": bson.M{"netdekomonid": loginId}}
								if err := mongo.UpdateDb(selector, update, "userInfos"); err != nil {
									logger.Write("failed netdekomonid update")

								} else {
									replyMessage = "ログインIDを " + loginId + " で登録しました。"
									logger.Write("success netdekomonid update")
								}
							}

						} else if strings.Contains(message.Text, "パスワード:") {
							password := strings.Replace(message.Text, " ", "", -1) // 全ての半角を消す
							password = strings.Replace(password, "パスワード:", "", 1)  // 頭のパスワード:消す

							// ネットDe勤怠のパスワードをDBに登録する パスワードは暗号化すること
							if password != "" {
								// TODO 暗号化処理
								// DB登録処理
								selector := bson.M{"userid": profile.UserID}
								update := bson.M{"$set": bson.M{"password": password}}
								if err := mongo.UpdateDb(selector, update, "userInfos"); err != nil {
									logger.Write("failed password update")

								} else {
									replyMessage = "パスワードを " + password + " で登録しました。\n" +
										"※暗号化して保存してます。"

									logger.Write("success password update")
								}
							}

						} else {
							replyMessage = usage
						}

						// 返信処理
						if replyMessage != "" {
							if _, err := bot.ReplyMessage(event.ReplyToken, linebot.NewTextMessage(replyMessage)).Do(); err != nil {
								logger.Write(err)
							}
						}

						logger.Write("message.Text: " + message.Text)
					}
				} else if event.Type == linebot.EventTypeFollow {
					userInfo := UserInfo{
						UserID:      profile.UserID,
						DisplayName: profile.DisplayName,
						IsCome:      false,
					}

					// ユーザ情報をDBに登録
					if err := mongo.InsertDb(userInfo, "userInfos"); err != nil {
						logger.Write(err)
					}

					// フレンド登録時のメッセージ
					var replyMessages [4]string

					replyMessages[0] = "メニューのボタンを押すだけで出社・退社ができます。\n" +
						"ご利用前には、かならずネットDe勤怠のIDとパスワードをご登録ください。"

					replyMessages[1] = "IDとパスワードを登録するには、下記のメッセージをコピペして送信してください。\n" +
						"フォーマットに誤りがあると登録できませんのでご注意ください。(再送すると情報を上書きできます。)"

					replyMessages[2] = "ログインID:<loginId>"

					replyMessages[3] = "パスワード:<password>"

					for _, replyMessage := range replyMessages {
						if _, err = bot.PushMessage(userId, linebot.NewTextMessage(replyMessage)).Do(); err != nil {
							logger.Write(err)
						}
					}
				}
			}

			// ブロック時の処理、ユーザ情報をDBから削除する
			if event.Type == linebot.EventTypeUnfollow {
				query := bson.M{"userid": userId}
				if err := mongo.RemoveDb(query, "userInfos"); err != nil {
					logger.Write(err)
				}
			}

			mongo.DisconnectDb()
			logger.Write("end event")
		}
	})

	// TODO
	//http.Handle("/image/", http.StripPrefix("/image/", http.FileServer(http.Dir("./"))))
	http.Handle("/lineDeKintai/callback", handler)

	if err := http.ListenAndServeTLS(":10443", apiIds.CertFile, apiIds.KeyFile, nil); err != nil {
		logger.Fatal("ListenAndServe: ", err)
	}

	// if err := http.ListenAndServe(":8080", nil); err != nil {
	// 	logger.Fatal(err)
	// }

}
